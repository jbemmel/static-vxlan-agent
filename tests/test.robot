*** Settings ***
Library     srl.py
Library     SSHLibrary
Library     String

*** Keywords ***
BGP Session Should Be Established
    [Arguments]      ${peer}
    ${result}=        get_bgp_neighbours   default     ${peer}

    Should Be Equal as strings   ${result['peer-as']}               65000  
    Should Be Equal as strings   ${result['peer-group']}            vxlan-agent  
    Should Be Equal as strings   ${result['peer-router-id']}        192.0.2.2  
    Should Be Equal as strings   ${result['session-state']}         established  
    Should Be Equal as strings   ${result['evpn']['admin-state']}   enable


Write SRL Command
    [Arguments]         ${cmd}

    ${tmp}=             Write               ${cmd}
    ${out}=             Read Until          [admin@srl2 ~]$
    [return]            ${out}

Open SRL Bash
    Open Connection     clab-static-vxlan-agent-dev-srl2    width=500
    Login               admin      admin
    Write               bash
    Read Until          [admin@srl2 ~]$

Agent Should Run
    ${result}=              get_agent_status
    Should Be Equal         ${result}   running


*** Test Cases ***
Test agent running when config applied
    # When not configured, agent is waiting for configuration
    ${result}=              get_agent_status
    Should Not Be Equal     ${result}   running

    # Now we configure the BGP neighbour and agent should come up
    setup_bgp_neighbour     1.1.1.4
    setup_agent             enable  1.1.1.4     1.1.1.4     65000   65000

    Wait Until Keyword Succeeds     30x     1s  
    ...    Agent Should Run 
    
    # Wait for the session the get established. We'll retry every 5s for a max of 10 times
    Wait Until Keyword Succeeds     30x     1s  
    ...    BGP Session Should Be Established   1.1.1.4

# Restarting the agent from the CLI kills all processes and they 
# relaunch without duplicates and the BGP session is re-established
#Test Restart Agent
#    Open SRL Bash
#    
#    # Check if the number of process for vxlan agent is equal to 6
#    # there should be 2 bash scripts and 2 process for the agent, 1 line for the ps/grep, and 1 for the prompt
#    ${out}=     Write SRL Command   ps aux | grep bin/static-vxlan-agent
#    @{lines}=   Split To Lines      ${out}    
#    Length Should Be     ${lines}    6
#        
#    # Now we restart the agent
#    restart_agent
#
#    # Wait for BGP Session to be established
#    setup_bgp_neighbour     1.1.1.4
#    # Wait for the session the get established. We'll retry every 5s for a max of 10 times
#    Wait Until Keyword Succeeds     10x     5s  
#    ...    BGP Session Should Be Established   1.1.1.4
#
#    # Verify number of processes again
#    ${out}=     Write SRL Command   ps aux | grep bin/static-vxlan-agent
#    @{lines}=   Split To Lines      ${out}    
#    Length Should Be     ${lines}    6

Test Create VRF
    setup_mac_vrf   242     210     210
    add_vtep        210     1.1.1.100
    add_vtep        210     1.1.1.101
    add_vtep        210     1.1.1.102
    add_vtep        210     1.1.1.103
    add_vtep        210     1.1.1.104
    delete_vtep     210     1.1.1.101
    delete_vtep     210     1.1.1.102
    @{paths}=       get_evpn_paths
    Length Should Be     ${paths}    3

    Should Contain    ${paths}    1.1.1.100:210
    Should Contain    ${paths}    1.1.1.103:210
    Should Contain    ${paths}    1.1.1.104:210
    
