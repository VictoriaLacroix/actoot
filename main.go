package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	//"log"
	"os"
	"strings"
	"sync"

	"9fans.net/go/acme"
	"github.com/k3a/html2text"
	//"github.com/mattermost/html2text"
	"github.com/mattn/go-mastodon"
)

var (
	app *mastodon.Application
	client *mastodon.Client
	instance string
)
var errCmdNotHandled = errors.New("cmd not handled")
var errCmdNotImplemented = errors.New("cmd not implemented")
var errIDNotKnown = errors.New("id not known")

func newWin(name, tag string) (*acme.Win, error) {
	win, err := acme.New()
	if err != nil {
		return nil, err
	}
	err = win.Name("%s", name)
	if err != nil {
		return nil, err
	}
	err = win.Fprintf("tag", tag)
	if err != nil {
		return nil, err
	}
	return win, nil
}

func getCommandArgs(evt *acme.Event) (string, string) {
	cmd := strings.TrimSpace(string(evt.Text))
	arg := strings.TrimSpace(string(evt.Arg))
	if arg == "" {
		parts := strings.SplitN(cmd, " ", 2)
		if len(parts) != 2 {
			arg = ""
		} else {
			arg = parts[1]
		}
		cmd = strings.TrimSpace(parts[0])
	}
	return cmd, arg
}

func extract(line, field string) string {
	return strings.TrimSpace(strings.TrimPrefix(line, field))
}

func parseToot(text string) (toot mastodon.Toot, err error) {
	var errbuf bytes.Buffer
	defer func() {
		if errbuf.Len() > 0 {
			err = errors.New(strings.TrimSpace(errbuf.String()))
		}
	}()

	off := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		off += len(line)
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		switch {
		case strings.HasPrefix(line, "#"):
			continue;
		case strings.HasPrefix(line, "Visibility:"):
			toot.Visibility = extract(line, "Visibility:")
		case strings.HasPrefix(line, "Spoiler:"):
			toot.SpoilerText = extract(line, "Spoiler:")
			toot.Sensitive = true
		case strings.HasPrefix(line, "InReplyTo:"):
			toot.InReplyToID = mastodon.ID(extract(line, "InReplyTo:"))
		default:
			fmt.Fprintf(&errbuf, "unknown summary line: %s\n", line)
		}
	}

	if errbuf.Len() > 0 {
		return
	}

	toot.Status = strings.TrimSpace(text[off:])

	return
}

func postStatus(win *acme.Win) error {
	body, err := win.ReadAll("body")
	if err != nil {
		return err
	}
	var post mastodon.Toot
	post, err = parseToot(string(body))
	if err != nil {
		return err
	}
	_, err = client.PostStatus(context.Background(), &post)
	if err != nil {
		return err
	}
	return nil
}

var newStatusTemplate = `Visibility: public
Spoiler:

<insert status here>
`

func composeNew(wg *sync.WaitGroup) {
	defer wg.Done()
	win, err := newWin("/actoot/new", "Put ")
	if err != nil {
		fmt.Printf("couldn't create window: %s", err)
		return
	}
	err = win.Fprintf("body", newStatusTemplate)
	for evt := range win.EventChan() {
		switch evt.C2 {
		case 'x', 'X':
			switch string(evt.Text) {
			case "Put":
				err = postStatus(win)
				if err != nil {
					win.Errf("couldn't post: %s", err)
					continue
				}
				win.Del(true)
			default:
				win.WriteEvent(evt)
			}
		default:
			win.WriteEvent(evt)
		}
	}
}

var newReplyTemplate = `#%s
InReplyTo: %s
Visibility: %s
Spoiler: %s

<insert reply here>
`

func printReplyTemplate(status *mastodon.Status) (string, error) {
	var buf bytes.Buffer
	var err error
	body := html2text.HTML2TextWithOptions(status.Content, html2text.WithLinksInnerText())
	for _, line := range strings.Split(body, "\n") {
		_, err = fmt.Fprintf(&buf, "# %s\n", line)
		if err != nil {
			return "", err
		}
	}
	_, err = fmt.Fprintf(&buf, "InReplyTo: %s\n", status.ID)
	if err != nil {
		return "", err
	}
	_, err = fmt.Fprintf(&buf, "Visibility: %s\n", status.Visibility)
	if err != nil {
		return "", err
	}
	if len(status.SpoilerText) > 0 {
		_, err = fmt.Fprintf(&buf, "Spoiler: %s\n", status.SpoilerText)
		if err != nil {
			return "", err
		}
	}
	_, err = fmt.Fprintf(&buf, "\n<insert reply here>\n")
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func composeReply(wg *sync.WaitGroup, id mastodon.ID) {
	defer wg.Done()
	win, err := newWin("/actoot/reply", "Put ")
	if err != nil {
		fmt.Printf("couldn't create window: %s", err)
		return
	}
	var inReplyTo *mastodon.Status
	inReplyTo, err = client.GetStatus(context.Background(), id)
	if err != nil {
		win.Errf("couldn't get status %s: %s", id, err)
		return
	}
	var template string
	template, err = printReplyTemplate(inReplyTo)
	if err != nil {
		win.Errf("couldn't print template: %s", err)
		return
	}
	err = win.Fprintf("body", template)
	if err != nil {
		win.Errf("couldn't print to window: %s", err)
		return
	}
	for evt := range win.EventChan() {
		switch evt.C2 {
		case 'x', 'X':
			switch string(evt.Text) {
			case "Put":
				err = postStatus(win)
				if err != nil {
					win.Errf("couldn't post: %s", err)
					continue
				}
				win.Del(true)
			default:
				win.WriteEvent(evt)
			}
		}
	}
}

func printToot(win *acme.Win, status *mastodon.Status) error {
	err := win.Fprintf("body", "Author: @%s\n", status.Account.Acct)
	if err != nil {
		return err
	}
	err = win.Fprintf("body", "CreatedAt: %s\n", status.CreatedAt)
	err = win.Fprintf("body", "URL: %s\n", status.URL)
	if len(status.SpoilerText) > 0 {
		err = win.Fprintf("body", "Subject: %s\n", status.SpoilerText)
		if err != nil {
			return err
		}
	}
	// FIXME: this rendering is kind of shit
	// it breaks @ and # by appending spaces, and pads parenthesized URLs preventing Acme from double-click selecting
	bodyText := html2text.HTML2TextWithOptions(status.Content, html2text.WithLinksInnerText())
	if err != nil {
		return err
	}
	err = win.Fprintf("body", "%s\n", bodyText)
	if err != nil {
		return err
	}
	for _, attachment := range(status.MediaAttachments) {
		err = win.Fprintf("body", "Attachment:\n")
		if err != nil {
			win.Errf("couldn't preview attachment: %s", err)
			continue
		}
		if len(attachment.Description) > 0 {
			err = win.Fprintf("body", "%q\n", attachment.Description)
			if err != nil {
				win.Errf("couldn't preview attachment: %s", err)
				continue
			}
		}
		err = win.Fprintf("body", "%s\n", attachment.RemoteURL)
		if err != nil {
			win.Errf("couldn't preview attachment: %s", err);
		}
	}
	err = win.Fprintf("body", "%d replies\t%d repeats\t%d favourites\n", status.RepliesCount, status.ReblogsCount, status.FavouritesCount)
	if err != nil {
		return err
	}
	return nil
}

func handleStatusCommand(wg *sync.WaitGroup, evt *acme.Event, id mastodon.ID) error {
	cmd, args := getCommandArgs(evt)
	if len(args) > 0 {
		id = mastodon.ID(args)
	}
	switch cmd {
	case "Boost":
		client.Reblog(context.Background(), id)
	case "Favourite":
		client.Favourite(context.Background(), id)
	case "Bookmark":
		client.Bookmark(context.Background(), id)
	case "Reply":
		wg.Add(1)
		go composeReply(wg, id)
	default:
		return errCmdNotHandled
	}
	return nil
}

func displayStatus(wg *sync.WaitGroup, status *mastodon.Status) {
	id := status.ID
	defer wg.Done()
	win, err := newWin("/actoot/status/" + string(status.ID), "Get Reply Boost Favourite Bookmark ")
	if err != nil {
		fmt.Printf("couldn't make new acme window: %s\n", err)
		return
	}
	err = printToot(win, status)
	if err != nil {
		win.Errf("couldn't print status: %s\n", err)
		return
	}
	for evt := range win.EventChan() {
		switch evt.C2 {
		case 'x', 'X':
			err = handleStatusCommand(wg, evt, id)
			if err == errCmdNotHandled {
				win.WriteEvent(evt)
			} else {
				win.Errf("command failed: %s", err)
			}
			continue
		case 'l', 'L':
			err := look(wg, win, string(evt.Text))
			switch err {
			case nil:
			case errIDNotKnown:
				err := win.WriteEvent(evt)
				if err != nil {
					win.Errf("can't write event: %s", err)
					continue
				}
			default:
				win.Errf("lookup failed: %s", err)
			}
		}
	}
}

func look(wg *sync.WaitGroup, win *acme.Win, text string) error {
	if strings.HasPrefix(text, "#") {
		timeline := strings.TrimLeft(text, "#")
		wg.Add(1)
		go displayTimeline(wg, timeline)
	} else if strings.HasPrefix(text, "@") {
		// FIXME: View account timeline
	} else {
		id := mastodon.ID(strings.Trim(text, " \r\t\n"))
		status, err := client.GetStatus(context.Background(), id)
		if err != nil {
			return errIDNotKnown
		}
		wg.Add(1)
		go displayStatus(wg, status)
	}
	return nil
}

func statusShort(status *mastodon.Status) (string, error) {
	var source *string
	hasCW := status.Sensitive || len(status.SpoilerText) > 0
	if hasCW {
		source = &status.SpoilerText
	} else {
		source = &status.Content
	}
	body := html2text.HTML2TextWithOptions(*source, html2text.WithLinksInnerText())
	if hasCW {
		body = fmt.Sprintf("Subject: %s", body)
	}
	splitPoint := strings.Index(body, "\n")
	if splitPoint > 0 {
		body = fmt.Sprintf("%s...", body[0:splitPoint])
	}
	if len(body) > 75 {
		body = fmt.Sprintf("%s...", strings.TrimSpace(body[0:75]))
	}
	return fmt.Sprintf("%s\t@%s\t%s\n%s", status.ID, status.Account.Acct, status.CreatedAt.String(), body), nil
}

func getTimeline(timeline string, pg *mastodon.Pagination) ([]*mastodon.Status, error) {
	if strings.HasPrefix(timeline, "#") {
		tag := strings.TrimLeft(timeline, "#")
		return client.GetTimelineHashtag(context.Background(), tag, false, nil)
	}
	switch timeline {
	case "direct":
		return client.GetTimelineDirect(context.Background(), pg)
	case "home":
		return client.GetTimelineHome(context.Background(), pg)
	case "local":
		return client.GetTimelinePublic(context.Background(), true, pg)
	case "federated":
		return client.GetTimelinePublic(context.Background(), false, pg)
	default:
		return []*mastodon.Status{}, fmt.Errorf("timeline not handled: %q", timeline)
	}
}

func printTimeline(win *acme.Win, statuses []*mastodon.Status) error {
	win.Clear()
	for _, status := range statuses {
		statusText, err := statusShort(status)
		if err != nil {
			win.Errf("couldn't summarize status: %s", err)
		}
		err = win.Fprintf("body", "%s\n", statusText)
		if err != nil {
			win.Errf("couldn't print status summary: %s", err)
			continue
		}
	}
	return nil
}

func displayTimeline(wg *sync.WaitGroup, timeline string) {
	defer wg.Done()
	win, err := newWin("/actoot/timeline/" + timeline, "Get More Compose")
	if err != nil {
		fmt.Printf("couldn't create window: %s", err)
		return
	}
	pagination := mastodon.Pagination{
		MaxID: "",
		SinceID: "",
		MinID: "",
		Limit: 100,
	}
	var statuses []*mastodon.Status
	statuses, err = getTimeline(timeline, &pagination)
	if err != nil {
		win.Errf("couldn't get timeline: %s", err)
		return
	}
	err = printTimeline(win, statuses)
	if err != nil {
		win.Errf("couldn't print timeline: %s", err)
		return
	}
	if len(statuses) == 0 {
		win.Errf("nothing to show")
		return
	}
	for evt := range win.EventChan() {
		switch evt.C2 {
		case 'x', 'X' :
			cmd, _ := getCommandArgs(evt)
			switch cmd {
			case "Get":
				var newStatuses []*mastodon.Status
				pagination.MinID = ""
				pagination.SinceID = pagination.MinID
				pagination.MaxID = ""
				pagination.Limit = 100
				newStatuses, err = getTimeline(timeline, &pagination)
				statuses = append(newStatuses, statuses...)
				if err != nil {
					win.Errf("couldn't refresh timeline: %s", err)
				}
				err = printTimeline(win, statuses)
				if err != nil {
					win.Errf("couldn't print timeline: %s", err)
				}
				continue
			case "More":
				var newStatuses []*mastodon.Status
				pagination.MinID = pagination.MaxID
				pagination.SinceID = ""
				pagination.MaxID = ""
				pagination.Limit = 100
				newStatuses, err = getTimeline(timeline, &pagination)
				statuses = append(statuses, newStatuses...)
				if err != nil {
					win.Errf("couldn't refresh timeline: %s", err)
				}
				err = printTimeline(win, statuses)
				if err != nil {
					win.Errf("couldn't print timeline: %s", err)
				}
				continue
			case "Compose":
				wg.Add(1)
				go composeNew(wg)
				continue
			default:
				err := win.WriteEvent(evt)
				if err != nil {
					win.Errf("can't write event: %s", err)
					return
				}
				continue
			}
		case 'l', 'L':
			err := look(wg, win, string(evt.Text))
			switch err {
			case nil:
			case errIDNotKnown:
				err := win.WriteEvent(evt)
				if err != nil {
					win.Errf("can't write event: %s", err)
					continue
				}
			default:
				win.Errf("lookup failed: %s", err)
			}
		}
	}
}

func register(instance string) error {
	// FIXME: Logging?
	fmt.Println("registering...")
	var err error
	app, err = mastodon.RegisterApp(context.Background(), &mastodon.AppConfig{
		Server: instance,
		ClientName: "actoot",
		Scopes: "read write follow",
		Website: "https://github.com/VictoriaLacroix/actoot",
	})
	if err != nil {
		return err
	}
	return nil
}

type Authentication struct {
	Instance string `json:string`
	ClientID string `json:string`
	ClientSecret string `json:string`
	Email string `json:string`
	AccessToken string `json:string`
}

func loadAuth() (Authentication, error) {
	auth := Authentication{}
	file, err := os.Open("auth.json")
	if err != nil {
		return auth, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&auth)
	if err != nil {
		return auth, err
	}
	return auth, nil
}

func saveAuth(auth Authentication) error {
	file, err := os.Create("auth.json")
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	err = encoder.Encode(auth)
	if err != nil {
		return err
	}
	return nil
}

func authenticateWithToken(auth Authentication) error {
	fmt.Println("authenticating with saved information...")
	client = mastodon.NewClient(&mastodon.Config{
		Server: auth.Instance,
		ClientID: auth.ClientID,
		ClientSecret: auth.ClientSecret,
		AccessToken: auth.AccessToken,
	})
	return nil
}

func authenticateWithPassword(instance, email, pass string) error {
	fmt.Println("authenticating...")
	client = mastodon.NewClient(&mastodon.Config{
		Server: instance,
		ClientID: app.ClientID,
		ClientSecret: app.ClientSecret,
	})
	err := client.Authenticate(context.Background(), email, pass)
	if err != nil {
		return err
	}
	return nil
}

func performFirstLogin() error {
	// FIXME: Use a better method of handling accounts
	var instance, htinstance, email, password string
	fmt.Printf("instance: ")
	count, err := fmt.Scanln(&instance)
	if err != nil {
		return err
	} else if count != 1 {
		return fmt.Errorf("too many tokens: %d, %q", count, instance)
	}
	if !strings.HasPrefix(instance, "http") {
		htinstance = fmt.Sprintf("https://%s/", instance)
		fmt.Printf("instance: %q\n", htinstance)
	} else {
		htinstance = instance
	}
	err = register(htinstance)
	if err != nil {
		return err
	}
	fmt.Printf("email: ")
	count, err = fmt.Scanln(&email)
	if err != nil {
		return err
	} else if count != 1 {
		return fmt.Errorf("too many tokens: %d, %q", count, email)
	}
	fmt.Printf("password: ")
	count, err = fmt.Scanln(&password)
	if err != nil {
		return err
	} else if count != 1 {
		return fmt.Errorf("too many tokens: %d, %q", count, password)
	}
	err = authenticateWithPassword(htinstance, email, password)
	if err != nil {
		return err
	}
	auth := Authentication{
		Instance: htinstance,
		ClientID: app.ClientID,
		ClientSecret: app.ClientSecret,
		Email: email,
		AccessToken: client.Config.AccessToken,
	}
	err = saveAuth(auth)
	if err != nil {
		return err
	}
	return nil
}

func login() error {
	auth, err := loadAuth()
	if err != nil {
		fmt.Printf("failed to load stored auth: %s\n", err)
		return performFirstLogin()
	}
	return authenticateWithToken(auth)
}

func main() {
	var wg sync.WaitGroup
	html2text.SetUnixLbr(true)
	err := login()
	if err != nil {
		fmt.Printf("couldn't authenticate: %s\n", err)
		return
	}
	fmt.Println("authenticated. loading timeline...")

	wg.Add(1)
	displayTimeline(&wg, "home")

	wg.Wait()
	fmt.Println("done.")
}
