package main

import (
	"fmt"
	"github.com/pyalex/hipchat"
	"strings"
)

func main() {
	user := "11111_22222"
	pass := "secret"
	resource := "bot"
	roomJid := "11111_room_name@conf.hipchat.com"
	fullName := "Some Bot"
	mentionName := "SomeBot"

	client, err := hipchat.NewClient(user, pass, resource)
	if err != nil {
		fmt.Printf("client error: %s\n", err)
		return
	}

	client.Status("chat")
	client.Join(roomJid, fullName)
	for message := range client.Messages() {
		if strings.HasPrefix(message.Body, "@"+mentionName) {
			client.Say(roomJid, fullName, "Hello")
		}
	}
}
