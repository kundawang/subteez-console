// Subteez Client Implementation

package subteez

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const searchEndpoint = "/api/search"
const detailsEndpoint = "/api/details"
const downloadEndpoint = "/api/download"

// MaxDownloadSize bounds the size of a single downloaded file (archive or
// plain subtitle). A response larger than this is aborted with
// ErrDownloadTooLarge instead of being buffered entirely in memory.
const MaxDownloadSize int64 = 100 * 1024 * 1024 // 100 MiB

type subteezClient struct {
	baseAddress string
	httpClient  http.Client
}

func NewClient(server string) ISubteezAPI {
	return &subteezClient{
		baseAddress: server,
		httpClient: http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// send given request to given endpoint
func (client subteezClient) sendRequest(endpoint string, request interface{}) ([]byte, error) {
	// serialize request to json string
	requestJson, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	// create new http request
	httpRequest, err := http.NewRequest(
		http.MethodPost,
		client.baseAddress+endpoint,
		bytes.NewBuffer(requestJson),
	)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Add("Content-Type", "application/json")

	// send http request
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	// return error when status code is not ok
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusNotFound:
			return nil, ErrNotFound
		case http.StatusBadRequest:
			return nil, ErrBadRequest
		case http.StatusInternalServerError:
			return nil, ErrServer
		}
		return nil, ErrUnhandledResponse(response.Status)
	}

	// read response body and return it
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return responseBody, nil
}

func (client subteezClient) Search(request SearchRequest) (*SearchResultResponse, error) {
	rawResponse, err := client.sendRequest(searchEndpoint, request)
	if err != nil {
		return nil, err
	}

	// deserialize response body
	var result SearchResultResponse
	if json.Unmarshal(rawResponse, &result); err != nil {
		return nil, err
	}

	if result.Status != StatusOk {
		return nil, ErrUnhandledResponse(result.Status)
	}
	return &result, nil
}

func (client subteezClient) GetDetails(request SubtitleDetailsRequest) (*SubtitleDetailsResponse, error) {
	rawResponse, err := client.sendRequest(detailsEndpoint, request)
	if err != nil {
		return nil, err
	}

	// deserialize response body
	var result SubtitleDetailsResponse
	if err = json.Unmarshal(rawResponse, &result); err != nil {
		return nil, err
	}
	if result.Status != StatusOk {
		return nil, ErrUnhandledResponse(result.Status)
	}
	return &result, nil
}

func (client subteezClient) Download(request SubtitleDownloadRequest) (string, []byte, error) {
	// generate download link
	parameters := url.Values{"id": {request.ID}}.Encode()
	url := client.baseAddress + downloadEndpoint + "?" + parameters

	// download file
	response, err := client.httpClient.Get(url)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()

	// return error code it's not ok
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusNotFound:
			return "", nil, ErrNotFound
		case http.StatusBadRequest:
			return "", nil, ErrBadRequest
		case http.StatusInternalServerError:
			return "", nil, ErrServer
		}
		return "", nil, ErrUnhandledResponse(response.Status)
	}

	// read at most MaxDownloadSize + 1 bytes so an oversized response is
	// detected deterministically instead of exhausting memory
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxDownloadSize+1))
	if err != nil {
		return "", nil, err
	}
	if int64(len(data)) > MaxDownloadSize {
		return "", nil, ErrDownloadTooLarge
	}

	// find file name by parsing "Content-Disposition" header; a missing or
	// malformed header falls back to a safe derived name instead of failing
	name := ""
	if contentDisposition := response.Header.Get("Content-Disposition"); contentDisposition != "" {
		if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
			name = params["filename"]
		}
	}
	if name == "" {
		// fall back to the last segment of the requested id, then to a
		// generic name, so a missing header never produces an empty file name
		name = "subtitle"
		if index := strings.LastIndex(request.ID, "/"); index >= 0 && index+1 < len(request.ID) {
			name = request.ID[index+1:]
		}
	}
	return name, data, nil
}
