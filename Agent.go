package main

import (
	"context"
	"time"
	"os/exec"
	"os/signal"
	"io"
	"os"
	"fmt"
	"sync"
	"syscall"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/nokia/srlinux-ndk-go/ndk"
	"google.golang.org/grpc"
)

type Vtep struct {
	Address string `json:"address"`
}

type VniConfig struct {
    AdminState string `json:"admin_state"`
    Vni string `json:"vni"`
    Evi string `json:"evi"`
    Vteps []Vtep `json:"vteps"`
}

type BgpConfig struct {
    AdminState string `json:"admin_state"`
    SourceAddress struct {
        Value string `json:"value"`
    }`json:"source_address"`
    PeerAddress struct {
        Value string `json:"value"`
    }`json:"peer_address"`
    LocalAS struct {
        Value string `json:"value"`
    }`json:"local_as"`
    PeerAS struct {
        Value string `json:"value"`
    }`json:"peer_as"`
    LocalPreference struct {
        Value string `json:"value"`
    }`json:"local_preference"`

}

type Agent struct {
	Name  string // Agent name
	AppID uint32

	speaker 	 *BGPSpeaker
    configManager   *ConfigurationManager
	gRPCConn     *grpc.ClientConn
	logger       *zerolog.Logger
	retryTimeout time.Duration

	// NDK Service clients
	SDKMgrServiceClient       ndk.SdkMgrServiceClient
	NotificationServiceClient ndk.SdkNotificationServiceClient
	TelemetryServiceClient    ndk.SdkMgrTelemetryServiceClient
	ChildProcess			  *exec.Cmd
	ChildStdin				  io.WriteCloser
}

func newAgent(ctx context.Context, name string, logger *zerolog.Logger) *Agent {
	//conn, err := grpc.Dial("localhost:50053", grpc.WithInsecure())
	conn, err := grpc.Dial("unix:///opt/srlinux/var/run/sr_sdk_service_manager:50053", grpc.WithInsecure())
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("gRPC connect failed")
	}

	// create SDK Manager Client
	sdkMgrClient := ndk.NewSdkMgrServiceClient(conn)
	// create Notification Service Client
	notifSvcClient := ndk.NewSdkNotificationServiceClient(conn)
	// create Telemetry Service Client
	telemetrySvcClient := ndk.NewSdkMgrTelemetryServiceClient(conn)

	// register agent
	// http://learn.srlinux.dev/ndk/guide/dev/go/#register-the-agent-with-the-ndk-manager
	r, err := sdkMgrClient.AgentRegister(ctx, &ndk.AgentRegistrationRequest{})
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Agent registration failed")
	}

	logger.Info().
		Uint32("app-id", r.GetAppId()).
		Str("name", name).
		Msg("Application registered successfully!")

	return &Agent{
		logger:                    logger,
        configManager:             NewConfigurationManager(logger),
		retryTimeout:              5 * time.Second,
		Name:                      name,
		AppID:                     r.GetAppId(),
		gRPCConn:                  conn,
		SDKMgrServiceClient:       sdkMgrClient,
		NotificationServiceClient: notifSvcClient,
		TelemetryServiceClient:    telemetrySvcClient,
	}
}

func (a *Agent) Run(ctx context.Context) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	wg := &sync.WaitGroup{}

	// Config notifications
	wg.Add(1)
	go func() {
		defer wg.Done()
		configChan := a.StartConfigNotificationStream(ctx)
		for {
			select {
			case notif := <-configChan:
                for _,n := range notif.GetNotification() {
                    a.configManager.processNotification(a, n.GetConfig())
                }
			case <-sigs:
				a.logger.Debug().Msg("Main process received SIGTERM")
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	//TODO DUMAIS: clearnup registration
	a.TerminateChildProcess()
}


func (a *Agent) TerminateChildProcess() {
	if a.ChildProcess != nil {
		a.logger.Info().Msg("Kill BGP Speaker")
		a.ChildProcess.Process.Signal(syscall.SIGTERM)
	}
}

func (a *Agent) SetChildProcess(cmd *exec.Cmd) {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr	

	a.ChildProcess = cmd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.logger.Info().Msg(fmt.Sprintf("Error getting stdin from BGP Speaker: %v", err))
	}

	err = cmd.Start()
	if err != nil {
		a.logger.Info().Msg(fmt.Sprintf("Error starting BGP Speaker: %v", err))
	}
	a.ChildStdin = stdin

}

func (a *Agent) SendToChildProcess(key string, data string) {
	if a.ChildStdin == nil {
		a.logger.Info().Msg("Can't write to BGP Speaker")
		return
	}
	fmt.Fprintf(a.ChildStdin, "{\"key\": \""+key+"\", \"data\": "+data+"}\n")
}

func (a *Agent) StartConfigNotificationStream(ctx context.Context) chan *ndk.NotificationStreamResponse {
	streamID := a.createNotificationSubscription(ctx)

	a.logger.Info().
		Uint64("stream-id", streamID).
		Msg("Notification stream created")

	notificationRegisterRequest := &ndk.NotificationRegisterRequest{
		Op:       ndk.NotificationRegisterRequest_AddSubscription,
		StreamId: streamID,
		SubscriptionTypes: &ndk.NotificationRegisterRequest_Config{ // config
			Config: &ndk.ConfigSubscriptionRequest{},
		},
	}

	streamChan := make(chan *ndk.NotificationStreamResponse)
	go a.startNotificationStream(ctx, notificationRegisterRequest, streamChan)

	return streamChan
}

// createNotificationSubscription creates a subscription and return the Stream ID.
// Stream ID is used to register notifications for other services.
func (a *Agent) createNotificationSubscription(ctx context.Context) uint64 {
	retry := time.NewTicker(a.retryTimeout)

	for {
		// get subscription and streamID
		notificationResponse, err := a.SDKMgrServiceClient.NotificationRegister(ctx,
			&ndk.NotificationRegisterRequest{
				Op: ndk.NotificationRegisterRequest_Create,
			})
		if err != nil || notificationResponse.GetStatus() != ndk.SdkMgrStatus_kSdkMgrSuccess {
			a.logger.Printf("agent %q could not register for notifications: %v. Status: %s", a.Name, err, notificationResponse.GetStatus().String())
			a.logger.Printf("agent %q retrying in %s", a.Name, a.retryTimeout)

			<-retry.C // retry timer
			continue
		}

		return notificationResponse.GetStreamId()
	}
}

func (a *Agent) startNotificationStream(ctx context.Context, req *ndk.NotificationRegisterRequest,
	streamChan chan *ndk.NotificationStreamResponse) {

	a.logger.Info().
		Uint64("stream-id", req.GetStreamId()).
		Str("subscription-type", subscriptionTypeName(req)).
		Msg("Starting streaming notifications")
	defer close(streamChan)

	retry := time.NewTicker(a.retryTimeout)
	stream := a.getNotificationStreamClient(ctx, req)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			ev, err := stream.Recv()
			if err == io.EOF {
				a.logger.Printf("agent %s received EOF for stream %v", a.Name, req.GetSubscriptionTypes())
				a.logger.Printf("agent %s retrying in %s", a.Name, a.retryTimeout)

				<-retry.C // retry timer
				continue
			}
			if err != nil {
				a.logger.Printf("agent %s failed to receive notification: %v", a.Name, err)

				<-retry.C // retry timer
				continue
			}
			streamChan <- ev
		}
	}

}

// subscriptionTypeName returns the name of the enclosed subscription type
func subscriptionTypeName(r *ndk.NotificationRegisterRequest) string {
	var sType string
	switch r.GetSubscriptionTypes().(type) {
	case *ndk.NotificationRegisterRequest_Config:
		sType = "config"
	case *ndk.NotificationRegisterRequest_Appid:
		sType = "app id"
	case *ndk.NotificationRegisterRequest_Route:
		sType = "route"
	case *ndk.NotificationRegisterRequest_BfdSession:
		sType = "bfd"
	case *ndk.NotificationRegisterRequest_Intf:
		sType = "interface"
	case *ndk.NotificationRegisterRequest_LldpNeighbor:
		sType = "lldp"
	case *ndk.NotificationRegisterRequest_Nhg:
		sType = "next-hop group"
	case *ndk.NotificationRegisterRequest_NwInst:
		sType = "network instance"
	}

	return sType
}

// getNotificationStreamClient acquires the notification stream client that is used to receive
// streamed notifications
func (a *Agent) getNotificationStreamClient(
	ctx context.Context,
	req *ndk.NotificationRegisterRequest) ndk.SdkNotificationService_NotificationStreamClient {

	retry := time.NewTicker(a.retryTimeout)

	var streamClient ndk.SdkNotificationService_NotificationStreamClient
	for {
		registerResponse, err := a.SDKMgrServiceClient.NotificationRegister(ctx, req)
		if err != nil || registerResponse.GetStatus() != ndk.SdkMgrStatus_kSdkMgrSuccess {
			a.logger.Printf("agent %s failed registering to notification with req=%+v: %v", a.Name, req, err)
			a.logger.Printf("agent %s retrying in %s", a.Name, a.retryTimeout)

			<-retry.C // retry timer
			continue

		}

		streamClient, err = a.NotificationServiceClient.NotificationStream(ctx,
			&ndk.NotificationStreamRequest{
				StreamId: req.GetStreamId(),
			})
		if err != nil {
			a.logger.Printf("agent %s failed creating stream client with req=%+v: %v", a.Name, req, err)
			a.logger.Printf("agent %s retrying in %s", a.Name, a.retryTimeout)
			time.Sleep(a.retryTimeout)

			<-retry.C // retry timer
			continue
		}

		return streamClient
	}
}
