package fakecloudflare

import (
	"encoding/xml"
	"io"
	"net/http"
	"sort"
	"strings"
)

// serveS3 is just enough of path-style S3 for backups: put, get and list.
// Signatures are not checked.
func (f *Fake) serveS3(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucketName, key, _ := strings.Cut(path, "/")
	f.Requests = append(f.Requests, "S3 "+r.Method+" "+bucketName)
	bucket := f.Buckets[bucketName]
	if bucket == nil {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	switch {
	case r.Method == "HEAD":
		if _, ok := bucket.Objects[key]; key != "" && !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	case r.Method == "PUT" && key != "":
		data, _ := io.ReadAll(r.Body)
		bucket.Objects[key] = data
		w.WriteHeader(http.StatusOK)
	case r.Method == "GET" && key != "":
		data, ok := bucket.Objects[key]
		if !ok {
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	case r.Method == "GET":
		type object struct {
			Key  string
			Size int
		}
		result := struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			Name        string
			KeyCount    int
			IsTruncated bool
			Contents    []object
		}{Name: bucketName}
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for key := range bucket.Objects {
			if strings.HasPrefix(key, prefix) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			result.Contents = append(result.Contents, object{Key: key, Size: len(bucket.Objects[key])})
		}
		result.KeyCount = len(keys)
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "NotImplemented", http.StatusNotImplemented)
	}
}
