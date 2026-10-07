package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"differ/internal/conversations"
	"differ/internal/git"
	"differ/internal/server"
	"differ/web"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hook":
			os.Exit(runHook(os.Args[2:]))
		case "install-hooks":
			os.Exit(installHooks(os.Args[2:]))
		}
	}
	repo := flag.String("repo", ".", "Path to a local Git working copy")
	base := flag.String("base", "", "Default base commit or ref (default: the branch's merge base with main)")
	head := flag.String("head", "", "Default head commit or ref (default: the checked-out branch)")
	addr := flag.String("addr", "127.0.0.1:7331", "Loopback address to listen on")
	db := flag.String("db", conversations.DefaultPath(), "SQLite database of captured Claude Code conversations ($DIFFER_DB)")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "Usage: differ [flags] [base-sha head-sha]")
		fmt.Fprintln(out, "       differ install-hooks [-repo path] [-local]   capture Claude Code conversations")
		fmt.Fprintln(out, "       differ hook (claude | post-rewrite KIND)     run by the installed hooks")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 2 {
		*base, *head = flag.Arg(0), flag.Arg(1)
	} else if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
		log.Fatal("-addr must use a loopback address, such as 127.0.0.1:7331")
	}
	root, err := filepath.Abs(*repo)
	if err != nil {
		log.Fatal(err)
	}
	explicitRepo := false
	flag.Visit(func(f *flag.Flag) { explicitRepo = explicitRepo || f.Name == "repo" })
	if r, err := git.Open(context.Background(), root); err == nil {
		root = r.Path
	} else if !explicitRepo {
		// Started outside a working copy: choose one in the browser instead.
		root = ""
	}
	if (*base == "") != (*head == "") {
		log.Fatal("Set both -base and -head, or neither")
	}
	assets, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *addr, Handler: server.New(server.Config{Repository: root, Base: *base, Head: *head, Database: *db}, assets), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Fatalf("Could not start Differ on %s: %v", srv.Addr, err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if root == "" {
		root = "none yet; choose one in the browser"
	}
	fmt.Printf("Differ is ready at http://%s\nRepository: %s\n", listener.Addr(), root)
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
