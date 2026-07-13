package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/net/webdav"

	"github.com/andolivieri/kittyfs/internal/webdavfs"
)

const shareName = "kittyfs"

// serves over WebDAV until interrupted
func cmdMount(o opts, args []string) error {
	fset := flag.NewFlagSet("mount", flag.ContinueOnError)
	addr := fset.String("addr", "localhost:8686", "address to serve WebDAV on (host:port)")
	basicAuth := fset.Bool("basic-auth", false, "require HTTP Basic Auth on the endpoint")
	httpUser := fset.String("http-user", "kitty", "Basic Auth username (with --basic-auth)")
	httpPass := fset.String("http-pass", "", "Basic Auth password (with --basic-auth)")
	if err := fset.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	vfs, err := openVolume(o)
	if err != nil {
		return err
	}

	handler := &webdav.Handler{
		// Serve under a named path so the Windows WebDAV redirector uses this
		// segment as the share name instead of its "DavWWWRoot" pseudo-folder.
		Prefix:     "/" + shareName,
		FileSystem: webdavfs.New(vfs),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "kittyfs mount: %s %s: %v\n", r.Method, r.URL.Path, err)
			}
		},
	}

	var h http.Handler = handler
	if *basicAuth {
		if *httpPass == "" {
			return fmt.Errorf("--basic-auth requires --http-pass")
		}
		h = withBasicAuth(handler, *httpUser, *httpPass)
	}

	srv := &http.Server{Addr: *addr, Handler: h}

	// Serve in the background so main can wait for a shutdown signal.
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
		close(serveErr)
	}()

	printMountHints(*addr, *basicAuth)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
	case <-stop:
		fmt.Fprintln(os.Stderr, "\nkittyfs mount: shutting down…")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "kittyfs mount: shutdown: %v\n", err)
	}

	if err := vfs.Flush(); err != nil {
		return fmt.Errorf("final flush: %w", err)
	}
	fmt.Fprintln(os.Stderr, "kittyfs mount: flushed and stopped.")
	return nil
}

func withBasicAuth(h http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="kittyfs"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func printMountHints(addr string, basicAuth bool) {
	url := "http://" + addr + "/" + shareName
	fmt.Printf("kittyfs: serving WebDAV at %s/  (Ctrl-C to stop)\n", url)
	if basicAuth {
		fmt.Println("kittyfs: HTTP Basic Auth is enabled — supply credentials when mounting.")
	}
	fmt.Println("kittyfs: bound to this host only; do not expose it to the network.")
	fmt.Println()
	fmt.Println("Mount it as a drive:")
	switch runtime.GOOS {
	case "windows":
		fmt.Printf("  net use K: %s /persistent:no\n", url)
		fmt.Println("  (requires the WebClient service: run  sc start webclient  if needed)")
		fmt.Println("  unmount with:  net use K: /delete")
	case "darwin":
		fmt.Println("  Finder → Go → Connect to Server… →", url)
		fmt.Printf("  # or:  mkdir -p /Volumes/kittyfs && mount_webdav -S %s/ /Volumes/kittyfs\n", url)
		fmt.Println("  unmount with:  umount /Volumes/kittyfs")
	default: // linux and friends
		fmt.Printf("  gio mount dav://%s/%s/\n", addr, shareName)
		fmt.Printf("  # or davfs2:  sudo mount -t davfs %s/ /mnt/kittyfs\n", url)
	}
	fmt.Println()
}
