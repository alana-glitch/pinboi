package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	DDate "github.com/Philiphil/Ddate-go"
	"github.com/bwmarrin/discordgo"
)

// TODO truncate

// Version is the current version, in format major.minor.patch
const Version = "1.3.0"

type guildPins struct {
	RefreshAt time.Time
	Pins      []*discordgo.Message
}

var token string
var guildTickers map[string](chan bool)
var guildTimers map[string]time.Duration
var guildPinCache map[string]guildPins

func init() {
	flag.StringVar(&token, "t", "", "Bot Token")
	flag.Parse()
}

func main() {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Printf("error creating Discord session: %s\n", err)
		return
	}
	defer dg.Close()

	rand.Seed(time.Now().UnixNano())
	guildTickers = make(map[string](chan bool))
	guildTimers = make(map[string]time.Duration)
	guildPinCache = make(map[string]guildPins)

	dg.AddHandler(messageCreate)

	// Open a websocket connection to Discord and begin listening.
	err = dg.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}

func creationTime(ID string) (t time.Time, err error) {
	i, err := strconv.ParseInt(ID, 10, 64)
	if err != nil {
		return
	}
	timestamp := (i >> 22) + 1420070400000
	t = time.Unix(timestamp/1000, 0)
	return
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID || !strings.HasPrefix(m.Content, "!pinboi") {
		return
	}

	if strings.HasPrefix(m.Content, "!pinboi start ") {
		if _, present := guildTickers[m.GuildID]; present {
			s.ChannelMessageSend(m.ChannelID, "Pin Boi already running!")
		} else {
			dur, err := time.ParseDuration(strings.TrimPrefix(m.Content, "!pinboi start "))
			if err != nil {
				s.ChannelMessageSend(m.ChannelID, "Error with format of start command. Usage: `!pinboi start 12h`")
				return
			}
			go startPinTicker(s, m.GuildID, dur)
			guildTimers[m.GuildID] = dur
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Pin Boi started! Running every `%s`.", dur.String()))
		}
	} else if m.Content == "!pinboi fetch" {
		if msgLoc, err := randomPinnedAll(s, m.GuildID); err != nil {
			fmt.Printf("Error getting random pinned message: %s\n", err)
		} else if echo, err := getMessageEcho(s, msgLoc); err != nil {
			fmt.Printf("Error creating echo: %s\n", err)
		} else {
			s.ChannelMessageSendComplex(m.ChannelID, echo)
		}
	} else if m.Content == "!pinboi stop" {
		if done, present := guildTickers[m.GuildID]; !present {
			s.ChannelMessageSend(m.ChannelID, "Pin Boi not running!")
		} else {
			done <- true
			delete(guildTickers, m.GuildID)
			delete(guildTimers, m.GuildID)
			s.ChannelMessageSend(m.ChannelID, "Pin Boi stopped!")
		}
	} else if m.Content == "!pinboi refresh" {
		if err := refreshPinCache(s, m.GuildID); err != nil {
			fmt.Printf("Error refreshing pins: %s\n", err)
			s.ChannelMessageSend(m.ChannelID, "500 internal server error")
		} else {
			s.ChannelMessageSend(m.ChannelID, "Pin Boi refreshed!")
		}
	} else if m.Content == "!pinboi status" {
		_, running := guildTickers[m.GuildID]
		duration, ok := guildTimers[m.GuildID]
		if !ok {
			duration = time.Duration(0)
		}
		pinCount := 0
		pins, ok := guildPinCache[m.GuildID]
		if ok {
			pinCount = len(pins.Pins)
		}

		s.ChannelMessageSend(m.ChannelID,
			fmt.Sprintf(`**Pin Boi Status Info**
Version: %s
Currently running: %v
Time between posting: %s
Pin count (+/- 12h): %v`,
				Version, running, duration.String(), pinCount))
	} else if strings.HasPrefix(m.Content, "!pinboi") {
		s.ChannelMessageSend(m.ChannelID,
			"Pin Boi is a bot to periodically repost pinned messages.\n"+
				"Pin Boi commands:\n"+
				"`!pinboi start 12h`: Starts the bot, if not started already. Can use hours, minutes, etc for time.\n"+
				"`!pinboi stop`: Stops the bot, if not stopped already.\n"+
				"`!pinboi fetch`: Fetch a random pin, now. Does not affect timer.\n"+
				"`!pinboi help`: Displays this message.\n"+
				"`!pinboi refresh`: Hard refresh cache.\n"+
				"`!pinboi status`: Display version, current timeout, pin count, and if running.")
	}

}

func startPinTicker(s *discordgo.Session, guildID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	done := make(chan bool)
	guildTickers[guildID] = done
	for {
		select {
		case <-done:
			fmt.Println("Done!")
			return
		case <-ticker.C:
			if msgLoc, err := randomPinnedAll(s, guildID); err != nil {
				fmt.Printf("Error getting random pinned message: %s\n", err)
			} else if echo, err := getMessageEcho(s, msgLoc); err != nil {
				fmt.Printf("Error creating echo: %s\n", err)
			} else {
				s.ChannelMessageSendComplex(msgLoc.ChannelID, echo)
			}
		}
	}
}

func getMessageEcho(s *discordgo.Session, loc messageLocation) (*discordgo.MessageSend, error) {
	echo := discordgo.MessageSend{}
	if msg, err := s.ChannelMessage(loc.ChannelID, loc.MessageID); err != nil {
		return &echo, fmt.Errorf("reading message: %w", err)
	} else if created, err := creationTime(loc.MessageID); err != nil {
		return &echo, fmt.Errorf("getting creation time: %w", err)
	} else if channel, err := s.Channel(loc.ChannelID); err != nil {
		return &echo, fmt.Errorf("resolving channel: %w", err)
	} else {
		dtime := DDate.TimeToDTime(created)
		echo.Content = fmt.Sprintf("`Written %s by %s in #%s`\n%s\n%s",
			fmt.Sprintf("%s %d, %d YOLD", dtime.Season, dtime.Day, dtime.Year),
			msg.Author.String(),
			channel.Name,
			loc.Link(),
			msg.Content,
		)

		if len(msg.Attachments) > 0 {
			resp, err := http.Get(msg.Attachments[0].URL)
			if err != nil {
				return nil, fmt.Errorf("fetching attachment: %w", err)
			}
			echo.Files = []*discordgo.File{
				&discordgo.File{
					Name:   msg.Attachments[0].Filename,
					Reader: resp.Body,
				},
			}
		}
	}
	fmt.Printf("Echo: %+v\n", echo)
	return &echo, nil
}

type messageLocation struct {
	GuildID, ChannelID, MessageID string
}

func (loc messageLocation) Link() string {
	return fmt.Sprintf("https://discordapp.com/channels/%s/%s/%s", loc.GuildID, loc.ChannelID, loc.MessageID)
}

func refreshPinCache(s *discordgo.Session, guildID string) error {
	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return fmt.Errorf("getting channels: %w", err)
	}
	guildPinInfo := guildPins{}
	pins := []*discordgo.Message{}
	for _, channel := range channels {
		fmt.Printf("Scanning channel %s\n", channel.Name)
		if channel.Type != discordgo.ChannelTypeGuildText {
			fmt.Printf("Passing over\n")
			continue
		}
		channelPins, err := s.ChannelMessagesPinned(channel.ID)
		if err != nil {
			return fmt.Errorf("getting channel pins: %w", err)
		}
		pins = append(pins, channelPins...)
		time.Sleep(time.Second)
	}
	guildPinInfo.Pins = pins
	guildPinInfo.RefreshAt = time.Now().Add(12 * time.Hour)
	guildPinCache[guildID] = guildPinInfo
	return nil
}

func randomPinnedAll(s *discordgo.Session, guildID string) (messageLocation, error) {
	guildPinInfo, ok := guildPinCache[guildID]
	if !ok || guildPinInfo.RefreshAt.Before(time.Now()) {
		if err := refreshPinCache(s, guildID); err != nil {
			return messageLocation{}, err
		}
	}

	pins := guildPinCache[guildID].Pins
	selected := pins[rand.Intn(len(pins))]
	return messageLocation{GuildID: guildID, ChannelID: selected.ChannelID, MessageID: selected.ID}, nil
}
