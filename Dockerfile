FROM alpine:3.3

ADD ./bin/correlation /app/
ADD ./bin/syncthing /app/

EXPOSE 5808
EXPOSE 5812

ENTRYPOINT ["/app/correlation"]
