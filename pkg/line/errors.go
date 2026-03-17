package line

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

var ErrLetterSealingRequired = errors.New("letter sealing must be enabled")

type talkExceptionData struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Code    int    `json:"code"`
	Reason  string `json:"reason"`
}

func parseTalkExceptionData(raw json.RawMessage) talkExceptionData {
	var data talkExceptionData
	_ = json.Unmarshal(raw, &data)
	return data
}

func parseHTTPAPIError(err error) (int, json.RawMessage, bool) {
	if err == nil {
		return 0, nil, false
	}

	msg := err.Error()
	idx := strings.Index(msg, "API error ")
	if idx == -1 {
		return 0, nil, false
	}

	rest := msg[idx+len("API error "):]
	statusText, bodyText, ok := strings.Cut(rest, ": ")
	if !ok {
		return 0, nil, false
	}

	status, convErr := strconv.Atoi(statusText)
	if convErr != nil {
		return 0, nil, false
	}

	return status, json.RawMessage(bodyText), true
}

func isLetterSealingLoginAPIError(err error) bool {
	status, body, ok := parseHTTPAPIError(err)
	if !ok || status != 400 {
		return false
	}

	var wrapper struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &wrapper); jsonErr != nil {
		return false
	}

	talk := parseTalkExceptionData(wrapper.Data)
	return wrapper.Code == 10051 &&
		strings.EqualFold(wrapper.Message, "RESPONSE_ERROR") &&
		strings.EqualFold(talk.Name, "TalkException") &&
		talk.Code == 20 &&
		strings.EqualFold(talk.Reason, "internal error")
}

func IsLetterSealingRequired(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrLetterSealingRequired) || isLetterSealingLoginAPIError(err)
}
