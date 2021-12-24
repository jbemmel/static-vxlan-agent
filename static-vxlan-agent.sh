#!/bin/sh

sig_term (){
    echo "Sending SIGTERM to main process with PID=$PID"
    kill -TERM $PID 2>/dev/null
}

trap sig_term SIGTERM SIGKILL
/opt/static-vxlan-agent/bin/static-vxlan-agent &
PID=$!
echo "Started main process with PID=$PID"
wait $PID
