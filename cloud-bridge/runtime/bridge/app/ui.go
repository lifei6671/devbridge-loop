package app

import (
	"net/http"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/web"
)

// UIHandler exposes the embedded admin UI.
func UIHandler() http.Handler {
	return web.Handler()
}
