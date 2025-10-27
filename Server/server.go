package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	pb "github.com/lordkjaer/chit-chat2.0/gRPC"
	"google.golang.org/grpc"
)

const (
	addr             = ":50051"
	maxMessageRunes  = 128
	componentServer  = "Server"
	eventBroadcast   = "Broadcast"
	eventJoin        = "Join"
	eventLeave       = "Leave"
	eventDeliveryErr = "DeliveryError"
)

// Logging to file setup
func setupFileLogging(prefix string) (*os.File, error) {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("%s-%s.log", prefix, time.Now().Format("20060102-150405"))
	path := filepath.Join("logs", filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	mw := io.MultiWriter(os.Stdout, f)
	log.SetOutput(mw)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return f, nil
}

type LamportClock struct {
	mu   sync.Mutex
	time int64
}

func (lc *LamportClock) Increment() int64 {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.time++
	return lc.time
}

func (lc *LamportClock) Update(received int64) int64 {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if received > lc.time {
		lc.time = received
	}
	lc.time++
	return lc.time
}

type chatServer struct {
	pb.UnimplementedChatServiceServer
	mu      sync.Mutex
	clients map[string]pb.ChatService_StreamMessagesServer
	clock   LamportClock
}

func newChatServer() *chatServer {
	return &chatServer{
		clients: make(map[string]pb.ChatService_StreamMessagesServer),
	}
}

func logEvent(event, clientID string, lamport int64, details string) {
	log.Printf("[%s] [EVENT=%s] [ClientID=%s] [Lamport=%d] %s",
		componentServer, event, clientID, lamport, details)
}

func (s *chatServer) broadcast(msg *pb.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, stream := range s.clients {
		if err := stream.Send(msg); err != nil {
			logEvent(eventDeliveryErr, id, msg.LogicalTime, fmt.Sprintf("send failed: %v (removing client)", err))
			delete(s.clients, id)
		}
	}
}

func (s *chatServer) validateMessage(m *pb.ChatMessage) error {
	if utf8.RuneCountInString(m.Message) > maxMessageRunes {
		return fmt.Errorf("message exceeds %d characters", maxMessageRunes)
	}
	return nil
}

func (s *chatServer) SendMessage(ctx context.Context, msg *pb.ChatMessage) (*pb.ChatResponse, error) {
	if err := s.validateMessage(msg); err != nil {
		return &pb.ChatResponse{Success: false, Error: err.Error()}, nil
	}

	lTime := s.clock.Update(msg.LogicalTime)
	msg.LogicalTime = lTime

	s.broadcast(msg)
	logEvent(eventBroadcast, msg.User, lTime, fmt.Sprintf("type=%s text=%q", msg.Type.String(), msg.Message))
	return &pb.ChatResponse{Success: true}, nil
}

func (s *chatServer) StreamMessages(req *pb.StreamRequest, stream pb.ChatService_StreamMessagesServer) error {
	clientID := req.GetRoom()

	s.mu.Lock()
	s.clients[clientID] = stream
	s.mu.Unlock()

	joinTime := s.clock.Increment()
	joinMsg := &pb.ChatMessage{
		User:        clientID,
		Message:     fmt.Sprintf("Participant %s joined Chit Chat at logical time %d", clientID, joinTime),
		LogicalTime: joinTime,
		Type:        pb.MessageType_JOIN,
	}
	s.broadcast(joinMsg)
	logEvent(eventJoin, clientID, joinTime, "client connected")

	<-stream.Context().Done()

	s.mu.Lock()
	delete(s.clients, clientID)
	s.mu.Unlock()

	leaveTime := s.clock.Increment()
	leaveMsg := &pb.ChatMessage{
		User:        clientID,
		Message:     fmt.Sprintf("Participant %s left Chit Chat at logical time %d", clientID, leaveTime),
		LogicalTime: leaveTime,
		Type:        pb.MessageType_LEAVE,
	}
	s.broadcast(leaveMsg)
	logEvent(eventLeave, clientID, leaveTime, "client disconnected")

	return nil
}

func main() {
	// setup file + console logging
	f, err := setupFileLogging("server")
	if err != nil {
		log.Fatalf("failed to init file logging: %v", err)
	}
	defer f.Close()

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterChatServiceServer(grpcServer, newChatServer())

	log.Printf("[%s] Server listening on %s", componentServer, addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("serve error: %v", err)
	}
}
