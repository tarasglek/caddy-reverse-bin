all : ok documentation lint

documentation : doc/index.html doc.go README.md 

lint : ok/index.html

ok : 
	mkdir ok

ok/%.html : doc/%.html
	tidy -quiet -output /dev/null $<
	touch $@

cov : all
	go test -v -coverprofile=coverage && go tool cover -html=coverage -o=coverage.html

tests :
	go test ./...

check :
	golint .
	go vet -all .
	gofmt -s -l .
	goreportcard-cli -v

README.md : doc/document.md
	pandoc --read=markdown --write=gfm < $< > $@

doc/index.html : doc/document.md doc/html.txt doc/caddy.xml
	pandoc --read=markdown --write=html --template=doc/html.txt \
		--metadata pagetitle="reverse-bin for Caddy" --syntax-definition=doc/caddy.xml < $< > $@

doc.go : doc/document.md doc/go.awk
	pandoc --read=markdown --write=plain $< | awk --assign=package_name=reversebin --file=doc/go.awk > $@
	gofmt -s -w $@

CADDY_BIN ?= ./caddy

build :
	go run github.com/caddyserver/xcaddy/cmd/xcaddy@latest build --output $(CADDY_BIN) --with github.com/tarasglek/reverse-bin=.
	$(CADDY_BIN) list-modules | grep http.handlers.reverse-bin
	$(CADDY_BIN) version

release-dry-run :
	$$(go env GOPATH)/bin/goreleaser release --snapshot --clean --skip=publish

clean :
	rm -f coverage.html coverage ok/* doc/index.html doc.go README.md
