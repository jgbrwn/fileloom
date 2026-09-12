(function () {
  "use strict";

  const keywordSets = {
    javascript: new Set("as async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield true false null undefined".split(" ")),
    typescript: new Set("as async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield true false null undefined interface type enum public private protected implements readonly namespace declare abstract".split(" ")),
    go: new Set("break default func interface select case defer go map struct chan else goto package switch const fallthrough if range type continue for import return var true false nil error".split(" ")),
    python: new Set("and as assert async await break case class continue def del elif else except finally for from global if import in is lambda match nonlocal not or pass raise return try while with yield True False None".split(" ")),
    bash: new Set("if then else elif fi for while in do done case esac function select time until true false".split(" ")),
    sql: new Set("select from where and or not insert into update delete create alter drop table values join inner left right on as group by order having limit null true false".split(" ")),
    json: new Set(["true", "false", "null"])
  };

  function escapeHTML(value) {
    return String(value).replace(/[&<>"']/g, function (character) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[character];
    });
  }

  function languageName(value) {
    value = String(value || "plaintext").toLowerCase();
    if (value === "js") return "javascript";
    if (value === "ts") return "typescript";
    if (value === "py") return "python";
    if (value === "sh" || value === "shell" || value === "shellscript") return "bash";
    if (value === "yml") return "yaml";
    return value;
  }

  function token(className, value) {
    return '<span class="fileloom-token-' + className + '">' + escapeHTML(value) + '</span>';
  }

  function highlightMarkup(source) {
    let output = "";
    let cursor = 0;
    while (cursor < source.length) {
      if (source.startsWith("<!--", cursor)) {
        const end = source.indexOf("-->", cursor + 4);
        const finish = end < 0 ? source.length : end + 3;
        output += token("comment", source.slice(cursor, finish));
        cursor = finish;
        continue;
      }
      if (source[cursor] === "<") {
        let finish = cursor + 1;
        let quote = "";
        while (finish < source.length) {
          const character = source[finish];
          if (quote) {
            if (character === quote && source[finish - 1] !== "\\") quote = "";
          } else if (character === '"' || character === "'") {
            quote = character;
          } else if (character === ">") {
            finish++;
            break;
          }
          finish++;
        }
        output += token("tag", source.slice(cursor, finish));
        cursor = finish;
        continue;
      }
      output += escapeHTML(source[cursor++]);
    }
    return output;
  }

  function highlight(source, language) {
    language = languageName(language);
    if (language === "plaintext" || language === "text" || language === "txt") return escapeHTML(source);
    if (language === "html" || language === "xml" || language === "svg") return highlightMarkup(source);
    const keywords = keywordSets[language] || new Set();
    let output = "";
    let cursor = 0;
    while (cursor < source.length) {
      const rest = source.slice(cursor);
      if (rest.startsWith("//") || rest.startsWith("/*") || (rest[0] === "#" && (language === "python" || language === "bash"))) {
        let finish = rest.startsWith("/*") ? source.indexOf("*/", cursor + 2) : source.indexOf("\n", cursor);
        if (finish < 0) finish = source.length;
        else if (rest.startsWith("/*")) finish += 2;
        output += token("comment", source.slice(cursor, finish));
        cursor = finish;
        continue;
      }
      if (language === "sql" && rest.startsWith("--")) {
        const finish = source.indexOf("\n", cursor);
        const end = finish < 0 ? source.length : finish;
        output += token("comment", source.slice(cursor, end));
        cursor = end;
        continue;
      }
      const character = source[cursor];
      if (character === '"' || character === "'" || character === "`") {
        const quote = character;
        let finish = cursor + 1;
        while (finish < source.length) {
          if (source[finish] === quote && source[finish - 1] !== "\\") {
            finish++;
            break;
          }
          finish++;
        }
        output += token("string", source.slice(cursor, finish));
        cursor = finish;
        continue;
      }
      if (/[0-9]/.test(character) && (cursor === 0 || !/[A-Za-z0-9_$]/.test(source[cursor - 1]))) {
        const match = source.slice(cursor).match(/^(?:0x[\da-f]+|\d+(?:\.\d+)?)/i);
        if (match) {
          output += token("number", match[0]);
          cursor += match[0].length;
          continue;
        }
      }
      if (/[A-Za-z_$]/.test(character)) {
        const match = source.slice(cursor).match(/^[A-Za-z_$][\w$]*/);
        const word = match ? match[0] : character;
        output += keywords.has(word) ? token("keyword", word) : escapeHTML(word);
        cursor += word.length;
        continue;
      }
      output += escapeHTML(character);
      cursor++;
    }
    return output;
  }

  function highlightAll(root) {
    (root || document).querySelectorAll("pre.fileloom-code-block, pre[data-fileloom-code], pre > code[class*='language-']").forEach(function (element) {
      const code = element.tagName.toLowerCase() === "code" ? element : element.querySelector("code");
      if (!code || code.dataset.fileloomHighlighted === "1") return;
      const language = code.dataset.language || (code.className.match(/language-([\w-]+)/) || [])[1] || element.dataset.language || "plaintext";
      code.innerHTML = highlight(code.textContent, language);
      code.dataset.fileloomHighlighted = "1";
      code.classList.add("fileloom-code-highlighted");
      if (element.tagName.toLowerCase() === "pre") element.dataset.language = language;
    });
  }

  function start() {
    highlightAll(document);
    if (window.MutationObserver) {
      const observer = new MutationObserver(function () { highlightAll(document); });
      observer.observe(document.body, { childList: true, subtree: true });
    }
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
  else start();
}());
