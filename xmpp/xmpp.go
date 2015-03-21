package xmpp

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"strings"
	"time"
)

const (
	NsJabberClient = "jabber:client"
	NsStream       = "http://etherx.jabber.org/streams"
	NsIqAuth       = "jabber:iq:auth"
	NsIqRoster     = "jabber:iq:roster"
	NsTLS          = "urn:ietf:params:xml:ns:xmpp-tls"
	NsSASL         = "urn:ietf:params:xml:ns:xmpp-sasl"
	NsBind         = "urn:ietf:params:xml:ns:xmpp-bind"
	NsSession      = "urn:ietf:params:xml:ns:xmpp-session"
	NsDisco        = "http://jabber.org/protocol/disco#items"
	NsMuc          = "http://jabber.org/protocol/muc"
	NsMucUser      = "http://jabber.org/protocol/muc#user"
	NsMucRoom      = "http://hipchat.com/protocol/muc#room"
	NsMamForward   = "urn:xmpp:forward:0"
	NsMam          = "urn:xmpp:mam:0"
	NsHTML         = "http://jabber.org/protocol/xhtml-im"
	NsXHTML        = "http://www.w3.org/1999/xhtml"

	xmlStream          = "<stream:stream from='%s' to='%s' version='1.0' xml:lang='en' xmlns='%s' xmlns:stream='%s'>"
	xmlStartTLS        = "<starttls xmlns='%s'/>"
	xmlStartSession    = "<iq type='set' id='%s'><session xmlns='%s'/></iq>"
	xmlIqSet           = "<iq type='set' id='%s'><query xmlns='%s'><username>%s</username><password>%s</password><resource>%s</resource></query></iq>"
	xmlAuth            = "<auth xmlns='%s' mechanism='PLAIN'>%s</auth>"
	xmlIqBind          = "<iq type='set' id='%s'><bind xmlns='%s'><resource>%s</resource></bind></iq>"
	xmlIqGet           = "<iq from='%s' to='%s' id='%s' type='get'><query xmlns='%s'/></iq>"
	xmlPresence        = "<presence from='%s'><show>%s</show></presence>"
	xmlMUCPresence     = "<presence id='%s' to='%s' from='%s'><x xmlns='%s'><history maxstanzas='%d'/></x></presence>"
	xmlHTMLBody        = "<html xmlns='%s'><body xmlns='%s'><p>%s</p><p>%s</p></body></html>"
	xmlHTMLImage       = "<img src='%s' title='%s' longdesc='%s##%s'/>"
	xmlMUCUnavailable  = "<presence id='%s' from='%s' to='%s' type='unavailable'/>"
	xmlMUCMessage      = "<message from='%s' id='%s' to='%s' type='groupchat'><body>%s</body>%s</message>"
	xmlPing            = "<iq from='%s' id='%s' type='get'><ping xmlns='urn:xmpp:ping'/></iq>"
	xmlIqHistoryFilter = "<field var='%s'><value>%s</value></field>"
	xmlIqHistory       = "<iq type='set' id='%s'><query xmlns='urn:xmpp:mam:0'><x xmlns='jabber:x:data'>%s</x><set xmlns='http://jabber.org/protocol/rsm'><max>%d</max></set></query></iq>"
)

type required struct{}

type features struct {
	XMLName    xml.Name  `xml:"features"`
	StartTLS   *required `xml:"starttls>required"`
	Mechanisms []string  `xml:"mechanisms>mechanism"`
}

type item struct {
	Jid         string `xml:"jid,attr"`
	Name        string `xml:"name,attr"`
	MentionName string `xml:"mention_name,attr"`
	Topic       string `xml:"topic"`
	Owner       string `xml:"owner"`
}

type query struct {
	XMLName xml.Name `xml:"query"`
	Items   []*item  `xml:"item"`
}

type body struct {
	Body string `xml:",innerxml"`
}

type Conn struct {
	incoming *xml.Decoder
	outgoing net.Conn
}

type Message struct {
	Jid         string
	MentionName string
	Body        string
}

type Attachment struct {
	ImageURL      string
	ImageFilename string
	ThumbnailSize string
	ThumbnailURL  string
}

type MessageDelay struct {
	Stamp string `xml:"stamp,attr"`
}

type IncomingMessage struct {
	XMLName  xml.Name     `xml:"message"`
	From     string       `xml:"from,attr"`
	To       string       `xml:"to,attr"`
	MID      string       `xml:"id,attr"`
	Body     string       `xml:"body"`
	Delay    MessageDelay `xml:"delay"`
	HTMLBody body         `xml:"html>body"`

	Invite *invite `xml:"x"`
	Result body    `xml:"result"`
	Fin    body    `xml:"fin"`
}

type invite struct {
	XMLName xml.Name `xml:"x"`
	From    string   `xml:"jid,attr"`
	Reason  string   `xml:"reason,attr"`
}

type xroom struct {
	Name  string `xml:"name"`
	Topic string `xml:"topic"`
}

type ForwardedMessage struct {
	XMLName xml.Name        `xml:"forwarded"`
	Message IncomingMessage `xml:"message"`
	Delay   MessageDelay    `xml:"delay"`
}

func (c *Conn) Stream(jid, host string) {
	fmt.Fprintf(c.outgoing, xmlStream, jid, host, NsJabberClient, NsStream)
}

func (c *Conn) StartTLS() {
	fmt.Fprintf(c.outgoing, xmlStartTLS, NsTLS)
}

func (c *Conn) UseTLS() {
	c.outgoing = tls.Client(c.outgoing, &tls.Config{InsecureSkipVerify: true})
	c.incoming = xml.NewDecoder(c.outgoing)
}

func (c *Conn) Auth(user string, pass string) {
	raw := "\x00" + user + "\x00" + pass
	enc := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(enc, []byte(raw))

	fmt.Fprintf(c.outgoing, xmlAuth, NsSASL, enc)
}

func (c *Conn) Bind(resource string) {
	fmt.Fprintf(c.outgoing, xmlIqBind, id(), NsBind, resource)
}

func (c *Conn) Features() *features {
	var f features
	c.incoming.DecodeElement(&f, nil)
	return &f
}

func (c *Conn) Next() (xml.StartElement, error) {
	var element xml.StartElement

	for {
		var err error
		var t xml.Token
		t, err = c.incoming.Token()
		if err != nil {
			return element, err
		}

		switch t := t.(type) {
		case xml.StartElement:
			element = t
			if element.Name.Local == "" {
				return element, errors.New("invalid xml response")
			}

			return element, nil
		}
	}
	panic("unreachable")
}

func (c *Conn) Discover(from, to string) {
	fmt.Fprintf(c.outgoing, xmlIqGet, from, to, id(), NsDisco)
}

func (c *Conn) Body(start *xml.StartElement) string {
	b := new(body)
	c.incoming.DecodeElement(b, start)
	return b.Body
}

func (c *Conn) Message(start *xml.StartElement) *IncomingMessage {
	m := new(IncomingMessage)
	c.incoming.DecodeElement(&m, start)
	return m
}

func (c *Conn) ForwardedMessage(start string) *ForwardedMessage {
	m := new(ForwardedMessage)
	xml.Unmarshal([]byte(start), &m)
	return m
}

func (c *Conn) Query() *query {
	q := new(query)
	c.incoming.DecodeElement(q, nil)
	return q
}

func (c *Conn) Invite(start string) *invite {
	i := new(invite)
	xml.Unmarshal([]byte(start), &i)
	if i.From == "" || i.Reason == "" {
		return nil
	}
	return i
}

func (c *Conn) Presence(jid, pres string) {
	fmt.Fprintf(c.outgoing, xmlPresence, jid, pres)
}

func (c *Conn) MUCPresence(roomId, jid string, history int) {
	fmt.Fprintf(c.outgoing, xmlMUCPresence, id(), roomId, jid, NsMuc, history)
}

func (c *Conn) MUCUnavailable(roomId, jid string) {
	fmt.Fprintf(c.outgoing, xmlMUCUnavailable, id(), jid, roomId)
}

func (c *Conn) MUCSend(to, from, body string, attachments []Attachment) {
	if len(attachments) > 0 {
		tags := []string{}
		for _, a := range attachments {
			tags = append(tags, fmt.Sprintf(xmlHTMLImage, a.ImageURL, a.ImageFilename, a.ThumbnailSize, a.ThumbnailURL))
		}
		html_body := fmt.Sprintf(xmlHTMLBody, NsHTML, NsXHTML, html.EscapeString(body), strings.Join(tags, "\n"))
		fmt.Fprintf(c.outgoing, xmlMUCMessage, from, id(), to, html.EscapeString(body), html_body)

	} else {
		fmt.Fprintf(c.outgoing, xmlMUCMessage, from, id(), to, html.EscapeString(body), "")
	}
}

func (c *Conn) Roster(from, to string) {
	fmt.Fprintf(c.outgoing, xmlIqGet, from, to, id(), NsIqRoster)
}

func (c *Conn) KeepAlive(from string) {
	fmt.Fprintf(c.outgoing, " ")
}

func (c *Conn) Close() error {
	return c.outgoing.Close()
}

func (c *Conn) History(jid string, start time.Time, limit int) {
	filters := []string{
		fmt.Sprintf(xmlIqHistoryFilter, "FORM_TYPE", NsMam),
		fmt.Sprintf(xmlIqHistoryFilter, "with", jid),
	}
	if !start.IsZero() {
		filters = append(filters, fmt.Sprintf(xmlIqHistoryFilter, "start", start.Format("2006-01-02T15:04:05Z")))
	}

	fmt.Fprintf(c.outgoing, xmlIqHistory, id(), strings.Join(filters, ""), limit)
}

func (c *Conn) Session() {
	fmt.Fprintf(c.outgoing, xmlStartSession, id(), NsSession)
}

func Dial(host string) (*Conn, error) {
	c := new(Conn)
	outgoing, err := net.Dial("tcp", host+":5222")

	if err != nil {
		return c, err
	}

	c.outgoing = outgoing
	c.incoming = xml.NewDecoder(outgoing)

	return c, nil
}

func ToMap(attr []xml.Attr) map[string]string {
	m := make(map[string]string)
	for _, a := range attr {
		m[a.Name.Local] = a.Value
	}

	return m
}

func id() string {
	b := make([]byte, 8)
	io.ReadFull(rand.Reader, b)
	return fmt.Sprintf("%x", b)
}
