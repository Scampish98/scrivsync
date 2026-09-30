package yandex

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestMkdirStopsAtNamespaceRoot(t *testing.T) {
	for _, prefix := range []string{"disk:", "app:"} {
		t.Run(prefix, func(t *testing.T) {
			var requests []string
			client := fixtureYandex(t, func(w http.ResponseWriter, r *http.Request) {
				p := r.URL.Query().Get("path")
				requests = append(requests, r.Method+" "+p)
				if p != prefix+"/Романы" && p != prefix+"/Романы/Backups" {
					t.Errorf("unexpected path: %q", p)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusNotFound)
				} else {
					w.WriteHeader(http.StatusCreated)
				}
			})

			for _, root := range []string{prefix, prefix + "/"} {
				if err := client.Mkdir(context.Background(), root); err != nil {
					t.Fatal(err)
				}
			}
			if len(requests) != 0 {
				t.Fatalf("root should not need HTTP requests: %v", requests)
			}

			if err := client.Mkdir(context.Background(), prefix+"/Романы/Backups"); err != nil {
				t.Fatal(err)
			}
			expected := []string{
				"GET " + prefix + "/Романы/Backups", "GET " + prefix + "/Романы",
				"PUT " + prefix + "/Романы", "PUT " + prefix + "/Романы/Backups",
			}
			if !reflect.DeepEqual(requests, expected) {
				t.Fatalf("requests = %v, want %v", requests, expected)
			}
		})
	}
}
