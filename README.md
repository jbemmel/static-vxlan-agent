#Architecture
The agent is split in two process:
    1: The main process which runs the agent that subscribes to the grpc server to receive events. 
    2: A child process, forked from the main process, that runs in the srbase-default netns. This child
       runs the bgp speaker code. Communication from the main process to the child is done through stdin

#Building Package For Production
#Installing
#Usage

#Automated Test Suite
run `make test`. This will startup the containerlab and run robot tests. To see the tests that will run, please refer to the tests folder.

#Development
##Build: 
To start the lab: `make redeploy-all`. This will build the agent, start the lab and add the agent into srl1 and srl2.
To ssh into srl1: make sshsrl1
To destroy the lab: make destroy-lab










