package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"kitchen_manager/handlers"
)

const (
	httpAddr  = ":8080"
	httpsAddr = ":8443"
	certFile  = "./cert.pem"
	keyFile   = "./key.pem"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./kitchen.db"
	}
	if err := openDB(dbPath); err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	if err := ensureCert(certFile, keyFile); err != nil {
		log.Fatal("tls cert:", err)
	}

	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	// HTTP → HTTPS redirect
	go func() {
		log.Println("HTTP redirect listening on", httpAddr)
		log.Fatal(http.ListenAndServe(httpAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := parseHost(r.Host)
			if err != nil {
				host = r.Host
			}
			http.Redirect(w, r, "https://"+host+httpsAddr+r.RequestURI, http.StatusMovedPermanently)
		})))
	}()

	log.Println("HTTPS listening on", httpsAddr)
	log.Fatal(http.ListenAndServeTLS(httpsAddr, certFile, keyFile, mux))
}

// parseHost splits host:port, returning just the host. If there is no port,
// returns the whole string and no error.
func parseHost(hostport string) (string, string, error) {
	host, port, err := net.SplitHostPort(hostport)
	return host, port, err
}
