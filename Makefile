#################
# Makefile to automate workflows used to instantiate Go-based dev environment
# and perform tasks required throughout the development process

# needs
# - docker-ce
# - containerlab
#################

APPNAME = static-vxlan-agent
GOPKGNAME := $(APPNAME)

SSHCMD=ssh -i ./id_rsa -o ConnectTimeout=5 -oStrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 
LABFILE = dev.clab.yml
BIN_DIR = $$(pwd)/build
BINARY = $$(pwd)/build/$(APPNAME)

# abs path of a dir that hosts makefile
ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# when make is called with `make cleanup=1 some-target` the CLEANUP var will be set to `--cleanup`
# this is used in clab destroy commands to remove the clab-dev lab directory 
CLEANUP=
ifdef cleanup
	CLEANUP := --cleanup
endif

init:
	mkdir -p yang logs/srl1 logs/srl2 build lab app
	
	docker run --rm -e APPNAME=${APPNAME} -v $$(pwd):/tmp hairyhenderson/gomplate:stable --input-dir /tmp/.gen --output-map='/tmp/{{ .in | strings.TrimSuffix ".tpl" }}'
	sudo chown -R $$(id -u):$$(id -g) .
	mv agent.yang yang/${APPNAME}.yang
	mv agent-config.yml ${APPNAME}.yml
	mv dev.clab.yml lab/
	cp .gen/*.go .

	sed -i 's/^APPNAME =.*$$/APPNAME = ${APPNAME}/g' Makefile
	cp .gen/.gitignore .

	go mod init ${GOPKGNAME}
	go fmt .
	go mod tidy

build-app: lint
	mkdir -p $(BIN_DIR)
	go build -o $(BINARY) -ldflags="-s -w" .
	cp $(APPNAME).sh $(BIN_DIR) 
	chmod 775 $(BIN_DIR)/$(APPNAME).sh

destroy-lab:
	cd lab; \
	sudo clab des -t $(LABFILE) $(CLEANUP); \
	sudo rm -f .*.clab.* \
	sudo rm -rf ../logs/*

keys: 
	test -f ./id_rsa || ssh-keygen -t rsa -f ./id_rsa -q -N ""
	sudo chmod 700 id_rsa
	sudo chmod 740 id_rsa.pub
	sudo chown 166898390:992  id_rsa.pub

deploy-lab: build-automated-test keys
	mkdir -p logs/srl1 logs/srl2
	cd lab; \
	sudo clab dep -t $(LABFILE)

redeploy-lab: destroy-lab deploy-lab

deploy-all: redeploy-all

redeploy-all: build-app redeploy-lab

# build an app and restart app_mgr without redeploying the lab
redeploy-app: build-app lint restart-app

show-app-status:
	cd lab; \
	sudo clab exec -t $(LABFILE) --label clab-node-kind=srl --cmd 'sr_cli "show system application $(APPNAME)"'

reload-app_mgr:
	cd lab; \
	sudo clab exec -t $(LABFILE) --label clab-node-kind=srl --cmd 'sr_cli "tools system app-management application app_mgr reload"'

restart-app:
	cd lab; \
	sudo clab exec -t $(LABFILE) --label clab-node-kind=srl --cmd 'sr_cli "tools system app-management application $(APPNAME) restart"'

compress-bin:
	rm -f build/compressed
	docker run --rm -w /stage -v $$(pwd):/stage gruebel/upx:latest --best --lzma -o build/compressed build/$(APPNAME)
	mv build/compressed build/$(APPNAME)

rpm: compress-bin
	docker run --rm -v $$(pwd):/tmp -w /tmp goreleaser/nfpm package \
	--config /tmp/nfpm.yml \
	--target /tmp/build \
	--packager rpm

clean: destroy-lab remove-files .gitignore
	# use default app name after clean
	sed -i 's/^APPNAME =.*$$/APPNAME = demo-app/g' Makefile

remove-files:
	sudo rm -rf logs build app lab yang *.yml *.go go.mod go.sum .gitignore

# create dev .gitignore
.ONESHELL:
.gitignore:
	cat <<- EOF > $@
	/*
	!.gitignore
	!.gen
	!LICENSE
	!Makefile
	!README.md

	!.vscode
	.vscode/*
	!.vscode/tasks.json
	EOF

lint-yang:
	docker run --rm -v $$(pwd):/yang ghcr.io/hellt/pyang pyang -Wnone -f tree -p yang/model yang/*.yang

lint-yaml:
	docker run --rm -v $$(pwd):/data cytopia/yamllint -d relaxed .

lint: lint-yang

run: redeploy-all

build-automated-test:
	cd tests && docker build -t static-vxlan-agent-automated-tests .

test: redeploy-all
	docker exec -ti clab-static-vxlan-agent-dev-test1 robot -b/mnt/debug.txt test.robot

sshsrl1: 
	$(SSHCMD) admin@clab-static-vxlan-agent-dev-srl1

sshsrl2: 
	$(SSHCMD) admin@clab-static-vxlan-agent-dev-srl2

sshtest: 
	$(SSHCMD) admin@clab-static-vxlan-agent-dev-test1
