package main
//TODO: what if we delete a vtep, a VNI config or the bgp config?
    // EVI is always the same for a VRF
    // There could be multiple VRF
//TODO: do we get multiple updates if more than one vteps? and if more that one vrf?
	// If so, then we might not wanna rebuild the whole list of paths. Maybe we should wait for "commit.end" before sending stuff to BGP??
//TODO: Are we allowed to have more than one static-vxlan-agent config under 1 VRF? 
	// if yes: then finding the vtep list using mac vrf won't work
	// if no: then why dont we just put the container directly under the mac-vrf?

import (
	"context"
	"os"

    "syscall"
	"strconv"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/metadata"
	//"google.golang.org/protobuf/encoding/prototext"
)

const (
	appName       = "static-vxlan-agent"
	logTimeFormat = "2006-01-02 15:04:05 MST"
)


func runAgent(ctx context.Context, logger *zerolog.Logger) {
	agent := newAgent(ctx, appName, logger)
    agent.Run(ctx)
}

func runBgpServer(ctx context.Context, logger *zerolog.Logger) {
	speaker := NewBGPSpeaker(logger)
    speaker.Run(ctx)
}


func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "agent_name", appName)

	// If the parent bash script gets killed, we don't wanna be orphaned. We wanna threat this as a SIGTERM
	syscall.RawSyscall(uintptr(157), uintptr(1), uintptr(syscall.SIGTERM), 0)

	// set logger parameters
	logger := zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: logTimeFormat,
		NoColor:    true,
	}).With().Timestamp().Logger()

	if len(os.Args) > 1 && os.Args[1] == "-c" {
		runBgpServer(ctx, &logger)
	} else {
		runAgent(ctx, &logger)
	}

}

func getUint32FromJson(val string) (uint32){
	u, _ := strconv.ParseUint(val, 10, 32)
	return uint32(u)
}

