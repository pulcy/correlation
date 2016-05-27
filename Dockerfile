FROM alpine:3.3

ADD ./bin/correlation /app/
ADD ./bin/syncthing /app/

ENTRYPOINT ["/app/correlation"]
