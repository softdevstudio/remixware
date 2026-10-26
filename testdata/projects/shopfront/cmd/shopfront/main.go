// Command shopfront serves the shop's storefront.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	fs := flag.NewFlagSet("shopfront", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "address to listen on")
	catalog := fs.String("catalog", "catalog.csv", "product list to serve")
	readTimeout := fs.Duration("read-timeout", 5*time.Second, "how long a client may take to send a request")
	writeTimeout := fs.Duration("write-timeout", 10*time.Second, "how long a response may take")
	cacheSize := fs.Int("cache", 512, "products to keep in memory")
	verbose := fs.Bool("verbose", false, "log every request")
	dryRun := fs.Bool("dry-run", false, "load the catalog and exit")
	fs.Parse(os.Args[1:])

	shop, err := loadShop(*catalog, *cacheSize)
	if err != nil {
		log.Fatalf("shopfront: %v", err)
	}
	if *dryRun {
		fmt.Printf("%d products loaded\n", shop.count())
		return
	}
	srv := &http.Server{
		Addr:         *addr,
		Handler:      shop.routes(*verbose),
		ReadTimeout:  *readTimeout,
		WriteTimeout: *writeTimeout,
	}
	log.Printf("shopfront listening on %s", *addr)
	log.Fatal(srv.ListenAndServe())
}
