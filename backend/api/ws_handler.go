// HTTP handler for websocket-based live mock interviews.
// Uses gorilla/websocket to upgrade the connection and stream
// question/transcript events in real time.

package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"foxvue-api/interview"
	"foxvue-api/models"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		allowed := os.Getenv("FRONTEND_URL")
		if allowed == "" {
			allowed = "http://localhost:5173"
		}
		return origin == allowed
	},
}

// wsMessage is the envelope for all WebSocket messages.
type wsMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// handleWebSocket upgrades the connection and drives a live interview session.
// Message types (client → server):
//
//	{ "type": "start",     "payload": { "jobDescription": "..." } }
//	{ "type": "transcript","payload": { "text": "..." } }
//	{ "type": "next" }
//
// Message types (server → client):
//
//	{ "type": "question",  "payload": { ...models.Question } }
//	{ "type": "feedback",  "payload": { ...models.Feedback } }
//	{ "type": "error",     "payload": { "message": "..." } }
func handleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	var jobDescription string

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// Client disconnected — normal exit.
			break
		}

		var msg wsMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			writeWS(conn, "error", gin.H{"message": "invalid message format"})
			continue
		}

		switch msg.Type {
		case "start":
			var payload struct {
				JobDescription string `json:"jobDescription"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				writeWS(conn, "error", gin.H{"message": "invalid start payload"})
				continue
			}
			jobDescription = payload.JobDescription
			q := interview.NextQuestion(jobDescription)
			writeWS(conn, "question", q)

		case "transcript":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				writeWS(conn, "error", gin.H{"message": "invalid transcript payload"})
				continue
			}
			feedback := interview.ScoreFeedback(payload.Text)
			writeWS(conn, "feedback", feedback)

		case "next":
			q := interview.NextQuestion(jobDescription)
			writeWS(conn, "question", q)

		default:
			writeWS(conn, "error", gin.H{"message": "unknown message type"})
		}
	}
}

func writeWS(conn *websocket.Conn, msgType string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	out, _ := json.Marshal(wsMessage{Type: msgType, Payload: raw})
	if err := conn.WriteMessage(websocket.TextMessage, out); err != nil {
		log.Printf("ws write error: %v", err)
	}
}

// Ensure models import is used.
var _ = models.Question{}
