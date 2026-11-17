package app

// serverVersion is the commit or release the binary was built from, set at
// build time (the Dockerfile's MISTY_SERVER_VERSION build argument):
//
//	go build -ldflags "-X github.com/misty-org/misty-server/internal/app.serverVersion=<commit>"
//
// Health reports "development" when it is unset.
var serverVersion string
