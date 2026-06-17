package main

import "net/http"

func main() {
	http.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("checkout"))
	})
	_ = http.ListenAndServe(":5050", nil)
}
