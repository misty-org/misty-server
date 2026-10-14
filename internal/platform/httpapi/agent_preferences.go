package api

// What an agent does with web search. These are the same for every account and every
// device: settings belong to the device and never reach the server, so a tool has no
// per-account preference to read. Web search is on whenever the model runtime is, runs
// on the default engine, and paid search apps are offered only when the user names one.
const (
	webSearchOnByDefault     = true
	offerSearchAppsByDefault = false
)
