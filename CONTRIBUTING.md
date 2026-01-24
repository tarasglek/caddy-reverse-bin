cd cmd/caddy && go build
ensure you have systemd-resolved, eg  apt install systemd-resolved -y
./examples/reverse-proxy/run.sh
curl -i python.localhost:9080
  