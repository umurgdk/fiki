FROM golang:1.11
WORKDIR /fiki/
COPY main.go .
COPY embed.go .
COPY static .
COPY go.mod .
RUN CGO_ENABLED=0 GOOS=linux go generate
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o fiki

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=0 /fiki/fiki .
CMD ["./fiki"]  

