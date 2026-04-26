package main

import (
	"crystal-disk-info-mp/internal/smart"
	"crystal-disk-info-mp/internal/web"
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:8080", "HTTP listen address")
	staticDir := flag.String("static", "", "static directory, defaults to 'static' next to executable")
	flag.Parse()

	web.SetupStaticDir(*staticDir)

	collector := smart.NewCollector()
	if err := collector.RequirePrivilege(); err != nil {
		if relaunchElevated(err) {
			return
		}
		log.Fatalf("%v", err)
	}

	server := web.NewServer(collector)
	server.Refresh(false, "", nil)
	fmt.Printf("CrystalDiskInfo MP listening on http://%s\n", *addr)
	if err := http.ListenAndServe(*addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}
