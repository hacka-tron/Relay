# Update server image. Build releases first (release build), then this
# image bakes the releases directory in. For a real deployment you would
# more likely mount or sync that directory from object storage.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /updateserver ./cmd/updateserver

FROM gcr.io/distroless/static-debian12
COPY --from=build /updateserver /updateserver
COPY releases /releases
ENV RELEASES_DIR=/releases PORT=8080
EXPOSE 8080
ENTRYPOINT ["/updateserver"]
