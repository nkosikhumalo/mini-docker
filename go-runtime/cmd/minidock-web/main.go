// minidock-web is a small static-file server intended for MiniDock Vite images.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	root := flag.String("root", "/usr/share/minidock/www", "static asset directory")
	address := flag.String("listen", "0.0.0.0:8080", "HTTP listen address")
	flag.Parse()

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("resolve static root: %v", err)
	}
	if info, err := os.Stat(filepath.Join(absoluteRoot, "index.html")); err != nil || !info.Mode().IsRegular() {
		log.Fatalf("static root %s must contain index.html", absoluteRoot)
	}
	files := http.FileServer(http.Dir(absoluteRoot))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), string(filepath.Separator))
		candidate := filepath.Join(absoluteRoot, name)
		if filepath.IsLocal(name) {
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				files.ServeHTTP(w, r)
				return
			}
		}
		// Vite single-page apps use client-side routes; serve index.html for
		// extensionless paths while keeping missing assets as real 404s.
		if filepath.Ext(r.URL.Path) == "" {
			http.ServeFile(w, r, filepath.Join(absoluteRoot, "index.html"))
			return
		}
		http.NotFound(w, r)
	})
	server := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Printf("MiniDock static frontend serving %s on %s\n", absoluteRoot, *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve frontend: %v", err)
	}
}
