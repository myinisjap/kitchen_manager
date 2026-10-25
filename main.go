package main

import (
	"log"
	"net"
	"net/http"
	"os"
	_ "time/tzdata"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/auth"
	"kitchen_manager/internal/database"
	"kitchen_manager/internal/tlscert"
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
	db, err := database.OpenDB(dbPath)
	if err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	hub := handlers.NewHub()
	go hub.Run()

	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db, hub)
	handlers.RegisterShopping(mux, db, hub)
	handlers.RegisterRecipes(mux, db, hub)
	handlers.RegisterRecipeImport(mux)
	handlers.RegisterReceipt(mux, db)
	handlers.RegisterCalendar(mux, db, hub)
	handlers.RegisterHistory(mux, db)
	handlers.RegisterSettings(mux, db)
	handlers.RegisterMeals(mux, db, hub)
	mux.HandleFunc("GET /ws", handlers.ServeWs(hub))
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	var handler http.Handler = mux

	if os.Getenv("OAUTH_ENABLED") == "true" {
		if os.Getenv("GOOGLE_CLIENT_ID") == "" || os.Getenv("GOOGLE_CLIENT_SECRET") == "" {
			log.Fatal("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set when OAUTH_ENABLED=true")
		}
		if os.Getenv("BASE_URL") == "" {
			log.Fatal("BASE_URL must be set when OAUTH_ENABLED=true")
		}
		if os.Getenv("OAUTH_ALLOWED_EMAILS") == "" {
			log.Fatal("OAUTH_ALLOWED_EMAILS must be set when OAUTH_ENABLED=true (comma-separated list of allowed emails)")
		}
		sm := auth.NewSessionManager(db)
		oauthCfg := auth.NewOAuthConfig()
		allowed := auth.AllowedEmails()

		mux.HandleFunc("/auth/login", auth.HandleLogin(sm, oauthCfg))
		mux.HandleFunc("/auth/callback", auth.HandleCallback(sm, oauthCfg, allowed))
		mux.HandleFunc("/auth/logout", auth.HandleLogout(sm))
		mux.HandleFunc("GET /api/me", auth.HandleMe(sm))

		handler = sm.LoadAndSave(auth.AuthMiddleware(sm, mux))
	} else {
		mux.HandleFunc("GET /api/me", auth.HandleMe(nil))
	}

	handler = securityHeaders(handler)

	if os.Getenv("SELF_SIGNED_TLS") == "true" {
		if err := tlscert.EnsureCert(certFile, keyFile); err != nil {
			log.Fatal("tls cert:", err)
		}
		go func() {
			log.Println("HTTP redirect listening on", httpAddr)
			log.Fatal(http.ListenAndServe(httpAddr, securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host, _, err := parseHost(r.Host)
				if err != nil {
					host = r.Host
				}
				http.Redirect(w, r, "https://"+host+httpsAddr+r.RequestURI, http.StatusMovedPermanently)
			}))))
		}()
		log.Println("HTTPS listening on", httpsAddr)
		log.Fatal(http.ListenAndServeTLS(httpsAddr, certFile, keyFile, handler))
	} else {
		log.Println("HTTP listening on", httpAddr)
		log.Fatal(http.ListenAndServe(httpAddr, handler))
	}
}

func parseHost(hostport string) (string, string, error) {
	host, port, err := net.SplitHostPort(hostport)
	return host, port, err
}

// contentSecurityPolicy allows only same-origin resources plus the Open Food
// Facts API (barcode lookups). 'unsafe-eval' is required by the standard
// Alpine.js build, which compiles x-* attribute expressions with new Function();
// the @alpinejs/csp build cannot evaluate the inline expressions used
// throughout static/index.html. No 'unsafe-inline' is needed: all scripts and
// styles are same-origin files. New external origins must be added here.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-eval'; " +
	"style-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"media-src 'self' blob: mediastream:; " +
	"connect-src 'self' https://world.openfoodfacts.org; " +
	"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets CSP and other hardening headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
