-include .env
export $(shell sed 's/=.*//' .env)

fossa-install:
	curl -H 'Cache-Control: no-cache' https://raw.githubusercontent.com/fossas/fossa-cli/master/install-latest.sh | bash
	
fossa-analyze:
	fossa analyze

gosec-install:
	curl -sfL https://raw.githubusercontent.com/securego/gosec/master/install.sh | sudo sh -s -- -b $(go env GOPATH)/bin v2.18.2

gosec:
	@echo https://github.com/securego/gosec
	gosec -fmt=json -out=gosec.json ./... 

gosec-display:
	gosec -stdout ./... 	
