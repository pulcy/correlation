FROM alpine:3.3

ADD ./correlation /app/
ADD ./syncthing /app/

ENTRYPOINT ["/app/correlation"]
