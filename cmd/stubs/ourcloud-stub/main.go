// OurCloud gRPC stub server for integration testing.
// This stub implements the BlockStorageAPI service with configurable responses.
//
// Usage:
//
//	ourcloud-stub -port 50051 -config fixtures.json
//
// The fixtures file configures users, consent lists, and endpoints.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Fixtures configures the stub's responses.
type Fixtures struct {
	Users map[string]UserFixture `json:"users"`
}

// UserFixture defines a test user's data.
type UserFixture struct {
	PublicSignKey  string           `json:"public_sign_key"`  // hex-encoded
	PublicCryptKey string           `json:"public_crypt_key"` // hex-encoded
	Consents       []string         `json:"consents"`         // usernames allowed to send pushes
	Endpoints      []EndpointFixture `json:"endpoints"`
}

// EndpointFixture defines a push endpoint.
type EndpointFixture struct {
	DeviceID string `json:"device_id"`
	FCMToken string `json:"fcm_token"`
}

// StubServer implements pb.BlockStorageAPIServer.
type StubServer struct {
	pb.UnimplementedBlockStorageAPIServer

	mu       sync.RWMutex
	fixtures Fixtures

	// Computed data stores
	labels map[string]*pb.Label       // label key (hex) -> Label
	blocks map[string][]byte          // block ID (hex) -> raw data
}

func NewStubServer() *StubServer {
	return &StubServer{
		labels: make(map[string]*pb.Label),
		blocks: make(map[string][]byte),
	}
}

// LoadFixtures loads and processes the fixtures file.
func (s *StubServer) LoadFixtures(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading fixtures file: %w", err)
	}

	if err := json.Unmarshal(data, &s.fixtures); err != nil {
		return fmt.Errorf("parsing fixtures: %w", err)
	}

	s.computeData()
	return nil
}

// computeData builds the labels and blocks maps from fixtures.
func (s *StubServer) computeData() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.labels = make(map[string]*pb.Label)
	s.blocks = make(map[string][]byte)

	// Root ID for user lookups: [31 zeros, 1]
	rootID := make([]byte, 32)
	rootID[31] = 1

	for username, user := range s.fixtures.Users {
		// Create UserAuth
		userAuth := &pb.UserAuth{
			FormatVersion:  &pb.FormatVersion{Value: 1},
			UserName:       username,
			PublicSignKey:  hexDecode(user.PublicSignKey),
			PublicCryptKey: hexDecode(user.PublicCryptKey),
		}

		// Store UserAuth as a block
		userAuthData, _ := proto.Marshal(userAuth)
		userAuthID := contentAddress(userAuthData)
		s.blocks[hexEncode(userAuthID)] = userAuthData

		// Create label for username lookup (root namespace)
		userLabelKey := computeLabelKey(rootID, username)
		s.labels[hexEncode(userLabelKey)] = &pb.Label{
			DataId: &pb.ID{Value: userAuthID},
		}

		// Compute owner ID (content address of UserAuth)
		ownerID := computeContentAddress(userAuth)

		// Create consent list
		consentList := &pb.PushConsentList{}
		for _, consentUser := range user.Consents {
			consentList.Consents = append(consentList.Consents, &pb.PushConsent{
				Username: consentUser,
			})
		}

		consentData, _ := proto.Marshal(consentList)
		consentID := contentAddress(consentData)
		s.blocks[hexEncode(consentID)] = consentData

		consentLabelKey := computeLabelKey(ownerID, fmt.Sprintf("/users/%s/platform/push/consents", username))
		s.labels[hexEncode(consentLabelKey)] = &pb.Label{
			DataId: &pb.ID{Value: consentID},
		}

		// Create endpoint list
		endpointList := &pb.PushEndpointList{}
		for _, ep := range user.Endpoints {
			endpointList.Endpoints = append(endpointList.Endpoints, &pb.PushEndpoint{
				DeviceId: ep.DeviceID,
				FcmToken: ep.FCMToken,
			})
		}

		endpointData, _ := proto.Marshal(endpointList)
		endpointID := contentAddress(endpointData)
		s.blocks[hexEncode(endpointID)] = endpointData

		endpointLabelKey := computeLabelKey(ownerID, fmt.Sprintf("/users/%s/platform/push/endpoints", username))
		s.labels[hexEncode(endpointLabelKey)] = &pb.Label{
			DataId: &pb.ID{Value: endpointID},
		}

		log.Printf("Loaded user %s: %d consents, %d endpoints", username, len(user.Consents), len(user.Endpoints))
	}
}

// GetBlock implements pb.BlockStorageAPIServer.
func (s *StubServer) GetBlock(ctx context.Context, req *pb.GetBlockRequest) (*pb.GetBlockResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if req.Id == nil {
		return &pb.GetBlockResponse{Found: false}, nil
	}

	key := hexEncode(req.Id.Value)
	data, ok := s.blocks[key]
	if !ok {
		log.Printf("GetBlock: not found %s", key[:16])
		return &pb.GetBlockResponse{Found: false}, nil
	}

	log.Printf("GetBlock: found %s (%d bytes)", key[:16], len(data))
	return &pb.GetBlockResponse{
		Found: true,
		Block: &pb.Datum{
			Data: &pb.Datum_RawData{
				RawData: &pb.RawData{Data: data},
			},
		},
	}, nil
}

// GetLabel implements pb.BlockStorageAPIServer.
func (s *StubServer) GetLabel(ctx context.Context, req *pb.GetLabelRequest) (*pb.GetLabelResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := hexEncode(req.Key)
	label, ok := s.labels[key]
	if !ok {
		log.Printf("GetLabel: not found %s", key[:16])
		return &pb.GetLabelResponse{Found: false}, nil
	}

	log.Printf("GetLabel: found %s", key[:16])
	return &pb.GetLabelResponse{
		Found: true,
		Label: label,
	}, nil
}

// Helper functions

func computeLabelKey(ownerID []byte, labelPath string) []byte {
	data := append(ownerID, []byte(labelPath)...)
	hash := sha256.Sum256(data)
	return hash[:]
}

func computeContentAddress(msg proto.Message) []byte {
	data, _ := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	return contentAddress(data)
}

func contentAddress(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func hexEncode(data []byte) string {
	return fmt.Sprintf("%x", data)
}

func hexDecode(s string) []byte {
	if s == "" {
		return make([]byte, 32) // Default to zeros
	}
	data := make([]byte, len(s)/2)
	for i := 0; i < len(data); i++ {
		fmt.Sscanf(s[i*2:i*2+2], "%02x", &data[i])
	}
	return data
}

func main() {
	port := flag.Int("port", 50051, "gRPC server port")
	fixturesPath := flag.String("config", "fixtures.json", "path to fixtures file")
	flag.Parse()

	server := NewStubServer()

	if _, err := os.Stat(*fixturesPath); err == nil {
		if err := server.LoadFixtures(*fixturesPath); err != nil {
			log.Fatalf("Failed to load fixtures: %v", err)
		}
	} else {
		log.Printf("No fixtures file at %s, starting with empty data", *fixturesPath)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterBlockStorageAPIServer(grpcServer, server)

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down...")
		grpcServer.GracefulStop()
	}()

	log.Printf("OurCloud stub listening on :%d", *port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
