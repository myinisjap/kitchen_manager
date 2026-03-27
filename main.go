package main

import (
	"log"
	"net/http"

	"kitchen_manager/handlers"
)

func main() {
	if err := openDB("./kitchen.db"); err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	mux := http.NewServeMux()

	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)

	mux.Handle("/", http.FileServer(http.Dir("./static")))

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
