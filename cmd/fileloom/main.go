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
	owner := flag.String("owner-email", os.Getenv("FILELOOM_OWNER_EMAIL"), "optional exe.dev email allowed into the CMS")
	flag.Parse()

	server, err := srv.New(*site, *web, *owner)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := server.Serve(*listen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
