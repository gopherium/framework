// SPDX-License-Identifier: Apache-2.0

package gospa_test

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gopherium/framework/gospa"
)

func ExampleApp() {
	admin, err := gospa.App(os.DirFS("dist"), gospa.AppOptions{
		HashedMaxAge: 30 * 24 * time.Hour,
		Meta:         []gospa.Meta{{Name: "api-root", Content: "/api/"}},
	})
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/admin/", http.StripPrefix("/admin", admin))
}

func ExampleFiles() {
	root, err := os.OpenRoot("uploads")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/media/", http.StripPrefix("/media", gospa.Files(root.FS(), gospa.FilesOptions{})))
}
