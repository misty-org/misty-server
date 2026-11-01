package api

// Paid search apps are offered only when the user names one. This is the same for every
// account and every device: settings belong to the device and never reach the server, so
// a tool has no per-account preference to read.
const offerSearchAppsByDefault = false
