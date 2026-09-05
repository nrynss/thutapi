package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPagesRenderColdShells(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
		path string
		want string
	}{
		{"shelf", Shelf, "/", "Make your own book"},
		{"interview", Interview, "/interview/abc", "data-interview-id=\"abc\""},
		{"book", Book, "/book/xyz", "data-book-id=\"xyz\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.name != "shelf" {
				r.SetPathValue("id", strings.TrimPrefix(tt.path, "/interview/"))
				if tt.name == "book" {
					r.SetPathValue("id", "xyz")
				}
			}
			w := httptest.NewRecorder()
			tt.h(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Fatalf("body lacks %q", tt.want)
			}
			for _, required := range []string{"/static/app.css", "width=device-width,initial-scale=1"} {
				if !strings.Contains(w.Body.String(), required) {
					t.Fatalf("body lacks %q", required)
				}
			}
			if tt.name != "book" && !strings.Contains(w.Body.String(), "/static/app.js") {
				t.Fatal("interactive shell lacks its module entry")
			}
		})
	}
}
