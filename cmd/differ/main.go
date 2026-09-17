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

	"differ/internal/server"
	"differ/web"
)

func main() {
	repo := flag.String("repo", ".", "Path to a local Git working copy")
	base := flag.String("base", "HEAD~1", "Default base commit or ref")
	head := flag.String("head", "HEAD", "Default head commit or ref")
	addr := flag.String("addr", "127.0.0.1:7331", "Loopback address to listen on")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: differ [flags] [base-sha head-sha]")
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
	assets, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *addr, Handler: server.New(server.Config{Repository: root, Base: *base, Head: *head}, assets), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
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
	fmt.Printf("Differ is ready at http://%s\nRepository: %s\n", listener.Addr(), root)
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
