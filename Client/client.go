package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	pb "github.com/VictorTroelsen/Chit-Chat/gRPC"
	"google.golang.org/grpc"
)

const (
	defaultAddr     = "localhost:50051"
	maxMessageRunes = 128
	componentClient = "Client"
	eventSend       = "Send"
	eventRecv       = "Receive"
	eventStart      = "Start"
	eventShutdown   = "Shutdown"
	eventValidation = "ValidationError"
)

func setupFileLoggingForClient(username string) (*os.File, error) {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("client-%s-%s.log", username, time.Now().Format("20060102-150405"))
	path := filepath.Join("logs", filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	// both stdout and file
	mw := io.MultiWriter(os.Stdout, f)
	log.SetOutput(mw)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return f, nil
}

type LamportClock struct {
	time int64
}

func (lc *LamportClock) Increment() int64 {
	lc.time++
	return lc.time
}

func (lc *LamportClock) Update(received int64) int64 {
	if received > lc.time {
		lc.time = received
	}
	lc.time++
	return lc.time
}

func logEvent(username, event string, lamport int64, details string) {
	log.Printf("[%s] [User=%s] [EVENT=%s] [Lamport=%d] %s",
		componentClient, username, event, lamport, details)
}

func validateUserMessage(msg string) error {
	if utf8.RuneCountInString(msg) > maxMessageRunes {
		return fmt.Errorf("message exceeds %d characters", maxMessageRunes)
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: client <username> [serverAddr]")
		os.Exit(1)
	}
	username := strings.TrimSpace(os.Args[1])
	serverAddr := defaultAddr
	if len(os.Args) >= 3 {
		serverAddr = os.Args[2]
	}

	// console logging
	f, err := setupFileLoggingForClient(username)
	if err != nil {
		log.Fatalf("failed to init file logging: %v", err)
	}
	defer f.Close()

	// connect gRPC
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("dial error: %v", err)
	}
	defer conn.Close()

	client := pb.NewChatServiceClient(conn)

	// Start stream to receive messages
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.StreamMessages(ctx, &pb.StreamRequest{Room: username})
	if err != nil {
		log.Fatalf("stream error: %v", err)
	}

	clock := &LamportClock{}
	logEvent(username, eventStart, clock.time, fmt.Sprintf("connected to %s", serverAddr))

	// receive messages from server
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		for {
			msg, err := stream.Recv()
			if err != nil {
				logEvent(username, eventShutdown, clock.time, fmt.Sprintf("stream closed: %v", err))
				return
			}
			l := clock.Update(msg.GetLogicalTime())

			fmt.Printf("[Lamport=%d] %s: %s\n", l, msg.GetUser(), msg.GetMessage())
			logEvent(username, eventRecv, l, fmt.Sprintf("type=%s from=%s text=%q",
				msg.GetType().String(), msg.GetUser(), msg.GetMessage()))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// read user input - send messages
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Type your message and press Enter. Ctrl+C to exit.")
	for {
		select {
		case <-sigCh:
			cancel()
			<-recvDone // wait until receiver finishes
			logEvent(username, eventShutdown, clock.time, "client exiting")
			return

		default:
			if !scanner.Scan() {
				cancel()
				<-recvDone
				logEvent(username, eventShutdown, clock.time, "stdin closed; exiting")
				return
			}
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			if err := validateUserMessage(text); err != nil {
				logEvent(username, eventValidation, clock.time, err.Error())
				fmt.Println("Error:", err)
				continue
			}

			l := clock.Increment()

			// Send chat message
			resp, err := client.SendMessage(context.Background(), &pb.ChatMessage{
				User:        username,
				Message:     text,
				LogicalTime: l,
				Type:        pb.MessageType_CHAT,
			})
			if err != nil {
				logEvent(username, eventSend, l, fmt.Sprintf("send failed: %v", err))
				fmt.Println("Send error:", err)
				continue
			}
			if !resp.GetSuccess() {
				logEvent(username, eventSend, l, fmt.Sprintf("server rejected: %s", resp.GetError()))
				fmt.Println("Server rejected:", resp.GetError())
				continue
			}
			logEvent(username, eventSend, l, fmt.Sprintf("text=%q", text))
		}
	}
}
