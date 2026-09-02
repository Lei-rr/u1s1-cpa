package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// hopHeaders are connection-scoped and must not be forwarded downstream.
var hopHeaders = map[string]bool{
	"connection":        true,
	"transfer-encoding": true,
	"content-encoding":  true,
	"content-length":    true,
	"keep-alive":        true,
}

func chatURL(s Storage) string { return s.APIBase() + "/v1/chat/completions" }

func requestPayload(req pluginapi.ExecutorRequest) []byte {
	if len(req.Payload) > 0 {
		return req.Payload
	}
	return req.OriginalRequest
}

func inferenceHeaders(callbackID string, s Storage, stream bool) http.Header {
	h := http.Header{}
	h.Set("content-type", "application/json")
	if stream {
		h.Set("accept", "text/event-stream")
	}
	if token := attestationToken(callbackID, s); token != "" {
		h.Set("x-u1s1-attestation", token)
	}
	return h
}

func forwardHeaders(in http.Header) http.Header {
	out := http.Header{}
	for k, v := range in {
		if hopHeaders[strings.ToLower(k)] {
			continue
		}
		out[k] = v
	}
	return out
}

// handleExecute performs a non-streaming completion.
func handleExecute(payload []byte) ([]byte, error) {
	req, err := decode[executorRequest](payload)
	if err != nil {
		return nil, err
	}
	s, ok := parseStorage(req.StorageJSON)
	if !ok {
		return nil, fmt.Errorf("u1s1 credential is missing a device key")
	}

	resp, err := do(req.HostCallbackID, s, http.MethodPost, chatURL(s),
		requestPayload(req.ExecutorRequest), inferenceHeaders(req.HostCallbackID, s, false))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamError(resp.StatusCode, resp.Body)
	}

	headers := forwardHeaders(resp.Headers)
	headers.Set("Content-Type", "application/json")
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: trimLeadingSpace(resp.Body),
		Headers: headers,
	})
}

// isChatCompletionsRoute reports whether the inbound request was addressed
// directly to the OpenAI chat completions endpoint.
//
// CPA's OpenAI handler wraps every stream chunk with "data: %s\n\n" downstream,
// so the passthrough path expects bare JSON. Conversely, all cross-protocol
// translators (OpenAI -> Claude / Gemini / Responses) expect framed
// "data: {...}\n\n" input and strip the prefix during translation.
func isChatCompletionsRoute(req pluginapi.ExecutorRequest) bool {
	raw, ok := req.Metadata["request_path"]
	if !ok || raw == nil {
		return true
	}
	path := ""
	switch v := raw.(type) {
	case string:
		path = strings.TrimSpace(v)
	case []byte:
		path = strings.TrimSpace(string(v))
	}
	if path == "" {
		return true
	}
	path = strings.Trim(path, "\"")
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/completions")
}

// handleExecuteStream opens an SSE completion and pumps frames downstream.
func handleExecuteStream(payload []byte) ([]byte, error) {
	req, err := decode[executorRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.StreamID) == "" {
		return nil, fmt.Errorf("stream_id is required")
	}
	s, ok := parseStorage(req.StorageJSON)
	if !ok {
		return nil, fmt.Errorf("u1s1 credential is missing a device key")
	}

	upstream, err := doStream(req.HostCallbackID, s, http.MethodPost, chatURL(s),
		requestPayload(req.ExecutorRequest), inferenceHeaders(req.HostCallbackID, s, true))
	if err != nil {
		return nil, err
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		body, _ := drainStream(upstream.StreamID)
		return nil, upstreamError(upstream.StatusCode, body)
	}

	bareJSON := isChatCompletionsRoute(req.ExecutorRequest)
	go pump(upstream.StreamID, req.StreamID, bareJSON)

	headers := http.Header{}
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	return okEnvelope(executorStreamResponse{Headers: headers})
}

// pump relays upstream SSE frames to the downstream CPA stream.
func pump(upstreamID, downstreamID string, bareJSON bool) {
	var failure string
	defer func() {
		_, _ = hostCall(methodHostStreamClose, hostStreamCloseRequest{StreamID: downstreamID, Error: failure})
		_, _ = hostCall(methodHostHTTPStreamClose, streamIDRequest{StreamID: upstreamID})
	}()

	var buf []byte
	for {
		chunk, done, err := readStream(upstreamID)
		if err != nil {
			failure = err.Error()
			return
		}
		buf = append(buf, chunk...)

		for {
			end, width := frameBoundary(buf)
			if end < 0 {
				break
			}
			frame := buf[:end+width]
			buf = buf[end+width:]
			if !emitFrame(downstreamID, frame, bareJSON, &failure) {
				return
			}
		}

		if done {
			if len(buf) > 0 {
				emitFrame(downstreamID, buf, bareJSON, &failure)
			}
			return
		}
	}
}

var doneMarker = []byte("[DONE]")
var dataPrefix = []byte("data:")

// emitFrame forwards one SSE frame, choosing bare JSON or framed SSE based
// on whether the host's OpenAI handler or a cross-protocol translator will consume it.
func emitFrame(downstreamID string, frame []byte, bareJSON bool, failure *string) bool {
	payload := frameData(frame)
	if len(payload) == 0 {
		return true // skip comment/empty frames
	}
	if bareJSON && bytes.Equal(payload, doneMarker) {
		return true // CPA OpenAI handler writes [DONE] itself
	}

	var toEmit []byte
	if bareJSON {
		toEmit = payload
	} else {
		// Cross-protocol translator path: must be valid SSE frame format
		toEmit = append(append([]byte("data: "), payload...), []byte("\n\n")...)
	}

	if _, err := hostCall(methodHostStreamEmit, hostStreamEmitRequest{
		StreamID: downstreamID,
		Payload:  toEmit,
	}); err != nil {
		*failure = err.Error()
		return false
	}
	return true
}

func frameData(frame []byte) []byte {
	var lines [][]byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		lines = append(lines, bytes.TrimSpace(line[len(dataPrefix):]))
	}
	if len(lines) == 0 {
		return nil
	}
	return bytes.TrimSpace(bytes.Join(lines, []byte("\n")))
}

func frameBoundary(buf []byte) (int, int) {
	lf := bytes.Index(buf, []byte("\n\n"))
	crlf := bytes.Index(buf, []byte("\r\n\r\n"))
	switch {
	case lf < 0 && crlf < 0:
		return -1, 0
	case crlf < 0:
		return lf, 2
	case lf < 0 || crlf < lf:
		return crlf, 4
	default:
		return lf, 2
	}
}

func readStream(streamID string) ([]byte, bool, error) {
	raw, err := hostCall(methodHostHTTPStreamRead, streamIDRequest{StreamID: streamID})
	if err != nil {
		return nil, true, err
	}
	var resp hostHTTPStreamReadResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, true, err
	}
	if resp.Error != "" {
		return nil, true, fmt.Errorf("%s", resp.Error)
	}
	return resp.Payload, resp.Done, nil
}

func drainStream(streamID string) ([]byte, error) {
	const maxBytes = 8 << 10
	var out []byte
	defer func() {
		_, _ = hostCall(methodHostHTTPStreamClose, streamIDRequest{StreamID: streamID})
	}()
	for len(out) < maxBytes {
		chunk, done, err := readStream(streamID)
		out = append(out, chunk...)
		if err != nil || done {
			return out, err
		}
	}
	return out, nil
}

func handleCountTokens(payload []byte) ([]byte, error) {
	req, err := decode[executorRequest](payload)
	if err != nil {
		return nil, err
	}
	tokens := int64(len(requestPayload(req.ExecutorRequest)) / 4)
	if tokens < 1 {
		tokens = 1
	}
	body, err := json.Marshal(map[string]int64{"total_tokens": tokens})
	if err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: body})
}

func handleHTTPRequest(payload []byte) ([]byte, error) {
	req, err := decode[executorHTTPRequest](payload)
	if err != nil {
		return nil, err
	}
	s, ok := parseStorage(req.StorageJSON)
	if !ok {
		return nil, fmt.Errorf("u1s1 credential is missing a device key")
	}

	target := req.URL
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		if !strings.HasPrefix(target, "/") {
			target = "/" + target
		}
		target = s.APIBase() + target
	}

	resp, err := do(req.HostCallbackID, s, req.Method, target, req.Body, req.Headers)
	if err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.ExecutorHTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    forwardHeaders(resp.Headers),
		Body:       resp.Body,
	})
}
