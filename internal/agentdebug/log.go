package agentdebug

import (
	"encoding/json"
	"os"
	"time"
)

// Log appends one NDJSON debug line for this session.
func Log(hypothesisID, location, message string, data map[string]any) {
	// #region agent log
	payload := map[string]any{
		"sessionId":    "25d741",
		"runId":        "post-fix",
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().UnixMilli(),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	line := append(b, '\n')
	const path = "/home/alex/operatots/.cursor/debug-25d741.log"
	_ = os.MkdirAll("/home/alex/operatots/.cursor", 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(line)
	// #endregion
}
