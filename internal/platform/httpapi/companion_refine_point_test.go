package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
	"time"
)

func companionRefineTestRequest(t *testing.T, width, height int) companionRefineRequest {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	var request companionRefineRequest
	request.Image.MimeType = "image/png"
	request.Image.DataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	request.Image.Width, request.Image.Height = width, height
	return request
}

func TestCompanionRefineImageMustMatchItsEnvelope(t *testing.T) {
	if _, err := decodeCompanionRefineImage(companionRefineTestRequest(t, 64, 48)); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*companionRefineRequest){
		"dimensions": func(r *companionRefineRequest) { r.Image.Width++ },
		"media type": func(r *companionRefineRequest) { r.Image.MimeType = "image/jpeg" },
		"too small":  func(r *companionRefineRequest) { *r = companionRefineTestRequest(t, 16, 16) },
		"too large":  func(r *companionRefineRequest) { *r = companionRefineTestRequest(t, 1100, 64) },
		"not base64": func(r *companionRefineRequest) { r.Image.DataURL = "data:image/png;base64,%%%" },
	} {
		request := companionRefineTestRequest(t, 64, 48)
		mutate(&request)
		if _, err := decodeCompanionRefineImage(request); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestCompanionRefineReadsOnlyAPointInsideTheCrop(t *testing.T) {
	if x, y, ok := parseCompanionRefine("sure [POINT:120, 45.5]", 400, 300); !ok || x != 120 || y != 45.5 {
		t.Fatalf("got %v %v %v", x, y, ok)
	}
	for _, text := range []string{"[POINT:none]", "no tag", "[POINT:401,10]", "[POINT:10,301]", "[POINT:-4,10]"} {
		if _, _, ok := parseCompanionRefine(text, 400, 300); ok {
			t.Fatalf("accepted %q", text)
		}
	}
}

func TestCompanionRefineCallsAreBoundedPerAnswer(t *testing.T) {
	now := time.Now()
	for i := 1; i <= companionRefineMaxCalls; i++ {
		if call, ok := admitCompanionRefine("invocation_refine_test", now); !ok || call != i {
			t.Fatalf("call %d refused", i)
		}
	}
	if _, ok := admitCompanionRefine("invocation_refine_test", now); ok {
		t.Fatal("refinements must be bounded")
	}
	if _, ok := admitCompanionRefine("invocation_refine_test", now.Add(companionRefineWindow+time.Second)); !ok {
		t.Fatal("an expired window must not keep counting")
	}
}
