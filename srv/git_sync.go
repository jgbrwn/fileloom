package srv

import (
	"errors"
	"log/slog"
	"strings"
	"time"
)

// GitCommandRunner is the command seam used by Fileloom's Git integration.
// Production uses the bounded runner in server.go; tests and embedders can
// replace it with a delayed or recording implementation before serving.
type GitCommandRunner func(timeout time.Duration, repo string, args ...string) (string, error)

type gitCommandRunner = GitCommandRunner

func (s *Server) SetGitCommandRunner(runner GitCommandRunner) {
	s.gitRunner = runner
}

func (s *Server) runGitCommand(timeout time.Duration, repo string, args ...string) (string, error) {
	if s.gitRunner != nil {
		return s.gitRunner(timeout, repo, args...)
	}
	return runGitWithTimeout(timeout, repo, args...)
}

func (s *Server) validateGitBranch(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "\r\n\t ") {
		return errors.New("branch name cannot contain whitespace")
	}
	output, err := s.runGitCommand(maxGitCommandDuration, "", "check-ref-format", "--branch", value)
	if err != nil {
		message := strings.TrimSpace(output)
		if message == "" {
			message = "invalid Git branch name"
		}
		return errors.New(message)
	}
	return nil
}

func (s *Server) queueGitSyncIfConfigured(trigger, message string) {
	config, err := s.loadSiteConfig()
	if err != nil {
		// Git is optional. A configuration read failure after a successful
		// mutation must be observable in logs, not turned into an HTTP failure.
		s.recordGitSyncResult(err)
		slog.Warn("git automation configuration read failed", "trigger", trigger, "error", err)
		return
	}
	if !config.Git.Enabled || !config.Git.AutoCommit || strings.ToLower(strings.TrimSpace(config.Git.CommitOn)) != trigger {
		return
	}
	s.enqueueGitSync(message)
}

func (s *Server) gitBuildIfConfigured(message string) {
	s.queueGitSyncIfConfigured("build", message)
}

func (s *Server) enqueueGitSync(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Update Fileloom site"
	}

	startWorker := false
	s.gitQueueMu.Lock()
	if s.gitSyncPending {
		s.gitSyncMessage = coalesceGitMessages(s.gitSyncMessage, message)
	} else {
		s.gitSyncPending = true
		s.gitSyncMessage = message
	}
	if !s.gitSyncRunning {
		s.gitSyncRunning = true
		startWorker = true
	}
	s.gitQueueMu.Unlock()

	if startWorker {
		go s.runGitSyncWorker()
	}
}

func coalesceGitMessages(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	if existing == "" {
		return next
	}
	if next == "" || existing == next {
		return existing
	}
	// A single automatic commit is allowed to contain changes from more than
	// one mutation. Do not claim that its message describes only one path.
	return "Update Fileloom site"
}

func (s *Server) runGitSyncWorker() {
	for {
		message, ok := s.takeGitSyncJob()
		if !ok {
			return
		}

		config, err := s.loadSiteConfig()
		if err == nil {
			if !config.Git.Enabled || !config.Git.AutoCommit {
				// Automation may have been disabled while a job was queued.
				continue
			}
			_, err = s.gitCommitAndPush(config.Git, message, false)
		}
		if err != nil {
			s.recordGitSyncResult(err)
			slog.Warn("automatic git sync failed", "error", err, "message", message)
		} else {
			s.recordGitSyncResult(nil)
		}
	}
}

func (s *Server) takeGitSyncJob() (string, bool) {
	s.gitQueueMu.Lock()
	defer s.gitQueueMu.Unlock()
	if !s.gitSyncPending {
		s.gitSyncRunning = false
		s.gitSyncMessage = ""
		return "", false
	}
	message := s.gitSyncMessage
	s.gitSyncPending = false
	s.gitSyncMessage = ""
	return message, true
}

func (s *Server) recordGitSyncResult(err error) {
	now := time.Now().UTC()
	s.gitQueueMu.Lock()
	defer s.gitQueueMu.Unlock()
	if err != nil {
		s.gitSyncLastError = err.Error()
		s.gitSyncLastErrorAt = now
		return
	}
	s.gitSyncLastError = ""
	s.gitSyncLastErrorAt = time.Time{}
	s.gitSyncLastSuccessAt = now
}

func (s *Server) cacheGitStatus(status GitStatus) {
	status.Pending = false
	status.Running = false
	status.LastError = ""
	status.LastErrorAt = ""
	status.LastSuccessAt = ""
	s.gitQueueMu.Lock()
	s.gitStatusCache = status
	s.gitStatusCacheValid = true
	s.gitQueueMu.Unlock()
}

func (s *Server) gitStatusWhileBusy() GitStatus {
	s.gitQueueMu.Lock()
	defer s.gitQueueMu.Unlock()
	status := GitStatus{}
	if s.gitStatusCacheValid {
		status = s.gitStatusCache
	}
	status.Error = "Git operation in progress"
	return status
}
func (s *Server) applyGitSyncStatus(status *GitStatus) {
	s.gitQueueMu.Lock()
	defer s.gitQueueMu.Unlock()
	status.Pending = s.gitSyncPending || s.gitSyncRunning
	status.Running = s.gitSyncRunning
	status.LastError = s.gitSyncLastError
	if !s.gitSyncLastErrorAt.IsZero() {
		status.LastErrorAt = s.gitSyncLastErrorAt.Format(time.RFC3339Nano)
	}
	if !s.gitSyncLastSuccessAt.IsZero() {
		status.LastSuccessAt = s.gitSyncLastSuccessAt.Format(time.RFC3339Nano)
	}
}
