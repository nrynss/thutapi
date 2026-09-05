// Package web serves Thutapi's server-rendered human pages.
package web

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var templates embed.FS

var pageTemplates = template.Must(template.ParseFS(templates, "templates/*.html"))

// Shelf serves the first screen. It remains useful when JavaScript is off.
func Shelf(w http.ResponseWriter, r *http.Request) {
	render(w, "shelf", pageData{})
}

// Interview serves the interview and generation screens for one interview.
func Interview(w http.ResponseWriter, r *http.Request) {
	render(w, "interview", pageData{InterviewID: r.PathValue("id")})
}

// Book is a temporary cold-link handoff. T10b replaces its contents.
func Book(w http.ResponseWriter, r *http.Request) {
	render(w, "book", pageData{BookID: r.PathValue("id")})
}

type pageData struct {
	InterviewID string
	BookID      string
}

func render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
	}
}
