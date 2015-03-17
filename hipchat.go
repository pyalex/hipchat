package hipchat

import (
	"errors"
	"github.com/pyalex/hipchat/xmpp"
	"log"
	"time"
)

var (
	Host = "chat.hipchat.com"
	Conf = "conf.hipchat.com"
)

// A Client represents the connection between the application to the HipChat
// service.
type Client struct {
	Username string
	Password string
	Resource string
	Id       string

	OnReconnect chan bool

	// private
	mentionNames    map[string]string
	connection      *xmpp.Conn
	receivedUsers   chan []*User
	receivedRooms   chan []*Room
	receivedMessage chan *Message

	messageBuffer   []Message
	recievedHistory chan []Message

	alive chan bool
}

// A Message represents a message received from HipChat.
type Message struct {
	From        string
	To          string
	Body        string
	MentionName string
	Stamp       time.Time
	Mid         string
}

// A User represents a member of the HipChat service.
type User struct {
	Id          string
	Name        string
	MentionName string
}

// A Room represents a room in HipChat the Client can join to communicate with
// other members..
type Room struct {
	Id    string
	Name  string
	Owner string
	Topic string
}

// NewClient creates a new Client connection from the user name, password and
// resource passed to it.
func NewClient(user, pass, resource string) (*Client, error) {
	connection, err := xmpp.Dial(Host)

	c := &Client{
		Username: user,
		Password: pass,
		Resource: resource,
		Id:       user + "@" + Host,

		// private
		connection:      connection,
		mentionNames:    make(map[string]string),
		receivedUsers:   make(chan []*User),
		receivedRooms:   make(chan []*Room),
		receivedMessage: make(chan *Message),
		OnReconnect:     make(chan bool),

		messageBuffer:   make([]Message, 0),
		recievedHistory: make(chan []Message),

		alive: make(chan bool),
	}

	if err != nil {
		return c, err
	}

	err = c.authenticate()
	if err != nil {
		return c, err
	}

	go c.listen()
	return c, nil
}

// Messages returns a read-only channel of Message structs. After joining a
// room, messages will be sent on the channel.
func (c *Client) Messages() <-chan *Message {
	return c.receivedMessage
}

// Rooms returns an slice of Room structs.
func (c *Client) Rooms() []*Room {
	c.requestRooms()
	return <-c.receivedRooms
}

// Users returns a slice of User structs.
func (c *Client) Users() []*User {
	c.requestUsers()
	return <-c.receivedUsers
}

// Status sends a string to HipChat to indicate whether the client is available
// to chat, away or idle.
func (c *Client) Status(s string) {
	c.connection.Presence(c.Id, s)
}

// Join accepts the room id and the name used to display the client in the
// room.
func (c *Client) Join(roomId, resource string, history int) {
	c.connection.MUCPresence(roomId+"/"+resource, c.Id, history)
}

func (c *Client) Leave(roomId, resource string) {
	c.connection.MUCUnavailable(roomId+"/"+resource, c.Id)
}

// Say accepts a room id, the name of the client in the room, and the message
// body and sends the message to the HipChat room.
func (c *Client) Say(roomId, name, body string) {
	c.connection.MUCSend(roomId, c.Id+"/"+name, body)
}

// KeepAlive is meant to run as a goroutine. It sends a single whitespace
// character to HipChat every 60 seconds. This keeps the connection from
// idling after 150 seconds.
func (c *Client) KeepAlive(nickname string) {
	go c.AliveChecker(nickname)
	for _ = range time.Tick(2 * time.Minute) {
		log.Println("keep alive")
		c.Join("1_default@"+Conf, nickname, 1)
	}
}

func (c *Client) AliveChecker(nickname string) {
	for {
		select {
		case <-c.alive:
			log.Println("alive")
			c.Leave("1_default@"+Conf, nickname)
		case <-time.After(5 * time.Minute):
			c.connection.Close()
		}
	}
}

func (c *Client) requestRooms() {
	c.connection.Discover(c.Id, Conf)
}

func (c *Client) requestUsers() {
	c.connection.Roster(c.Id, Host)
}

func (c *Client) LoadHistory(roomJid string, start time.Time, limit int) []Message {
	c.connection.History(roomJid, start, limit)
	return <-c.recievedHistory
}

func (c *Client) authenticate() error {
	c.connection.Stream(c.Id, Host)
	for {
		element, err := c.connection.Next()
		if err != nil {
			return err
		}

		switch element.Name.Local + element.Name.Space {
		case "stream" + xmpp.NsStream:
			features := c.connection.Features()
			if features.StartTLS != nil {
				c.connection.StartTLS()
			} else {
				for _, m := range features.Mechanisms {
					if m == "PLAIN" {
						c.connection.Auth(c.Username, c.Password)
					}
				}
			}
		case "proceed" + xmpp.NsTLS:
			c.connection.UseTLS()
			c.connection.Stream(c.Id, Host)

		case "success" + xmpp.NsSASL:
			c.connection.Stream(c.Id, Host)
			c.connection.Bind(c.Resource)
			c.connection.Session()

		case "failure" + xmpp.NsSASL:
			return errors.New("could not authenticate")

		case "iq" + xmpp.NsJabberClient:
			for _, attr := range element.Attr {
				if attr.Name.Local == "type" && attr.Value == "result" {
					return nil // authenticated
				}
			}

			return errors.New("could not authenticate")
		}
	}

	return errors.New("unexpectedly ended auth loop")
}

func (c *Client) reconnect() {
	log.Println("Reconnecting...")
	err := c.connection.Close()
	if err != nil {
		log.Println(err)
	}

	time.Sleep(1 * time.Minute)
	connection, err := xmpp.Dial(Host)
	if err != nil {
		panic(err)
	}

	c.connection = connection
	c.authenticate()

	log.Println("New connection")
	c.Status("available")
	c.OnReconnect <- true
}

func strtotime(str string) time.Time {
	stamp, err := time.Parse("2006-01-02T15:04:05Z", str)
	if err != nil {
		stamp = time.Now()
	}
	return stamp
}

func (c *Client) listen() {
	for {
		element, err := c.connection.Next()
		if err != nil {
			log.Println("Smth went wrong", err)
			c.reconnect()
			continue
		}

		switch element.Name.Local + element.Name.Space {
		case "iq" + xmpp.NsJabberClient: // rooms and rosters
			if c.connection.Body(&element) == "" {
				continue
			}

			query := c.connection.Query()
			switch query.XMLName.Space {
			case xmpp.NsMucRoom:
				items := make([]*Room, len(query.Items))
				for i, item := range query.Items {
					items[i] = &Room{Id: item.Jid, Name: item.Name,
						Owner: item.Owner, Topic: item.Topic}
				}
				c.receivedRooms <- items
			case xmpp.NsIqRoster:
				items := make([]*User, len(query.Items))
				for i, item := range query.Items {
					items[i] = &User{Id: item.Jid, Name: item.Name, MentionName: item.MentionName}
				}
				c.receivedUsers <- items
			}
		case "message" + xmpp.NsJabberClient:
			log.Println("incoming..")
			next, err := c.connection.Next()
			if err != nil {
				continue
			}
			switch next.Name.Local {
			case "x":
				log.Println("invite")
				invite := c.connection.Invite(&element)
				if invite == nil {
					continue
				}

				items := make([]*Room, 1)
				items[0] = &Room{Id: invite.RoomJid, Name: invite.Room.Name,
					Owner: invite.Invite.From, Topic: invite.Room.Topic}
				//c.receivedRooms <- items
			case "result":
				continue
			case "fin":
				c.recievedHistory <- c.messageBuffer
				c.messageBuffer = c.messageBuffer[:0]
			case "body":
				m := c.connection.Message(&element)

				//c.alive <- true
				c.receivedMessage <- &Message{
					From:  m.From,
					To:    m.To,
					Body:  m.Body,
					Mid:   m.MID,
					Stamp: strtotime(m.Delay.Stamp),
				}
			}
		case "forwarded" + xmpp.NsMamForward:
			log.Println("forwarded")
			forwarded := c.connection.ForwardedMessage(&element)

			c.messageBuffer = append(c.messageBuffer, Message{
				From:  forwarded.Message.From,
				To:    forwarded.Message.To,
				Body:  forwarded.Message.Body,
				Mid:   forwarded.Message.MID,
				Stamp: strtotime(forwarded.Delay.Stamp),
			})
		default:
			log.Println(element.Name.Local, element.Name.Space, element.Attr)
		}
	}
}
