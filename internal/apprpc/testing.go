package apprpc

import "net/http"

const TestingMailJSONLimit = mailJSONLimit

func TestingForwardJournalUploadCredential(destination, source http.Header, method string) error {
	return forwardJournalUploadCredential(destination, source, method)
}
