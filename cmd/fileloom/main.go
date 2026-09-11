package main

import (
	"flag"
	"fmt"
	"os"

	"fileloom/srv"
)

func main() {
	listen := flag.String("listen", ":8000", "address to listen on")
	site := flag.String("site", "site", "site workspace directory")
	web := flag.String("web", "web", "web assets directory")
	owner := flag.String("owner-email", os.Getenv("FILELOOM_OWNER_EMAIL"), "exe.dev email allowed to access /_cms")
	baseURL := flag.String("base-url", os.Getenv("FILELOOM_BASE_URL"), "canonical public URL used in feeds and sitemaps")
	flag.Parse()

	server, err := srv.NewWithOptions(*site, *web, *owner, *baseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := server.Serve(*listen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
