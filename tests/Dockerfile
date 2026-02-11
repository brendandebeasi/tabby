FROM golang:1.24-bullseye
RUN apt-get update && apt-get install -y ca-certificates tmux
COPY . /plugin
WORKDIR /plugin
RUN mkdir -p /root/.tmux/plugins/tabby /root/.config/tabby
RUN cp config.yaml /root/.config/tabby/
RUN ln -s /plugin /root/.tmux/plugins/tabby
RUN go build -o bin/render-status cmd/render-status/main.go
RUN go build -o bin/sidebar-renderer cmd/sidebar-renderer/main.go
RUN go build -o bin/tabby-daemon cmd/tabby-daemon/main.go
CMD ["/bin/bash"]
