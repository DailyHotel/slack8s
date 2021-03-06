package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nlopes/slack"
)

// The GET request to the Kubernetes event watch API returns a JSON object
// which unmarshals into this Response type.
type Response struct {
	Type   string `json:"type"`
	Object Event  `json:"object"`
}

// The Event type and its child-types, contain only the values of the response
// that our alerts currently care about.
type Event struct {
	Source         EventSource         `json:"source"`
	InvolvedObject EventInvolvedObject `json:"involvedObject"`
	Metadata       EventMetadata       `json:"metadata"`
	Reason         string              `json:"reason"`
	Message        string              `json:"message"`
	FirstTimestamp time.Time           `json:"firstTimestamp"`
	LastTimestamp  time.Time           `json:"lastTimestamp"`
	Count          int                 `json:"count"`
}

type EventMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type EventSource struct {
	Component string `json:"component"`
}

type EventInvolvedObject struct {
	Kind string `json:"kind"`
}

func filter_event(metadataName string, eventReason string) bool {
	reason := os.Getenv("EVENT_REASON")
	pods := os.Getenv("POD_NAMES")

	complete := false

	// @todo 여러 reason 보낼 수 있게 수정이 필요함.
	if eventReason == reason {
		complete = true
	} else {
		return complete
	}


	if len(pods) > 0 {
		names := strings.Split(pods, ",")
		for _, name := range names {
			if strings.Contains(metadataName, name) {
				complete = true
				return complete
			} else {
				complete = false
			}
		}
	}

	return complete
}

func custom_deploy_attachment(e Event) slack.Attachment {
	imageTag := strings.TrimPrefix(e.Message, "Successfully pulled image ")

	attachment := slack.Attachment{
		// The fallback message shows in clients such as IRC or OS X notifications.
		Fallback: e.Message,
		Fields: []slack.AttachmentField{
			slack.AttachmentField{
				Title: "Pod-Name",
				Value: e.Metadata.Name,
			},
			slack.AttachmentField{
				Title: "Deployed-Image-Tag",
				Value: imageTag,
			},
		},
	}

	return attachment
}

func general_attachment(e Event) slack.Attachment {
	attachment := slack.Attachment{
		// The fallback message shows in clients such as IRC or OS X notifications.
		Fallback: e.Message,
		Fields: []slack.AttachmentField{
			slack.AttachmentField{
				Title: "Namespace",
				Value: e.Metadata.Namespace,
				Short: true,
			},
			slack.AttachmentField{
				Title: "Message",
				Value: e.Message,
			},
			slack.AttachmentField{
				Title: "Object",
				Value: e.InvolvedObject.Kind,
				Short: true,
			},
			slack.AttachmentField{
				Title: "Name",
				Value: e.Metadata.Name,
				Short: true,
			},
			slack.AttachmentField{
				Title: "Reason",
				Value: e.Reason,
				Short: true,
			},
			slack.AttachmentField{
				Title: "Component",
				Value: e.Source.Component,
				Short: true,
			},
		},
	}

	return attachment
}

// Sends a message to the Slack channel about the Event.
func send_message(e Event, color string) error {
	api := slack.New(os.Getenv("SLACK_TOKEN"))
	params := slack.PostMessageParameters{}

	attachment := custom_deploy_attachment(e);

	// Use a color if provided, otherwise try to guess.
	if color != "" {
		attachment.Color = color
	} else if strings.HasPrefix(e.Reason, "Success") {
		attachment.Color = "good"
	} else if strings.HasPrefix(e.Reason, "Fail") {
		attachment.Color = "danger"
	}

	params.Attachments = []slack.Attachment{attachment}

	channelID, timestamp, err := api.PostMessage(os.Getenv("SLACK_CHANNEL"), "", params)
	if err != nil {
		fmt.Printf("%s\n", err)
		return err
	}

	log.Printf("Message successfully sent to channel %s at %s", channelID, timestamp)
	return nil
}

func main() {
	namespace := os.Getenv("EVENT_NAMESPACE")

	url := "http://localhost:8001/api/v1"
	if len(namespace) > 0 {
		url += "/namespaces/" + namespace
	}
	url += "/events?watch=true"

	req, err := http.NewRequest("GET", fmt.Sprintf(url), nil)
	if err != nil {
		log.Fatal("NewRequest: ", err)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal("Do: ", err)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf(string(resp.Status) + ": " + string(resp.StatusCode))
		log.Fatal("Non 200 status code returned from Kubernetes API.")
	}
	for {
		var r Response
		if err := dec.Decode(&r); err == io.EOF {
			log.Printf("EOF detected.")
			break
		} else if err != nil {
			// Debug output to help when we've failed to decode.
			htmlData, er := ioutil.ReadAll(resp.Body)
			if er != nil {
				log.Printf("Already failed to decode, but also failed to read response for log output.")
			}
			log.Printf(string(htmlData))
			log.Fatal("Decode: ", err)
		}
		e := r.Object

		send := false
		color := ""

		if filter_event(e.Metadata.Name, e.Reason) {
			send = true
			color = "good"
		}

		// For now, dont alert multiple times, except if it's a backoff
		if e.Count > 1 {
			send = false
		}

		// Do not send any events that are more than 1 minute old.
		// This assumes events are processed quickly (very likely)
		// in exchange for not re-notifying of events after a crash
		// or fresh start.
		diff := time.Now().Sub(e.LastTimestamp)
		diffMinutes := int(diff.Minutes())
		if diffMinutes > 1 && !strings.Contains(e.Message, "killed") {
			log.Printf("Supressed %s minute old message: %s", strconv.Itoa(diffMinutes), e.Message)
			send = false
		}

		// elastalert detect "killed" log word..

		if send {
			err = send_message(e, color)
			if err != nil {
				log.Fatal("send_message: ", err)
			}
		}
	}
}
