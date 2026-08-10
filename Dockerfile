# One static binary in an empty image. ADR-0009 chose a pure-Go SQLite driver,
# which is what makes CGO_ENABLED=0 possible here — no libc, no base image, no
# distro CVE feed to track for a program that is one file.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /comms .

# The data directory has to exist in the image, owned by the user that will run
# as. A named volume mounted over an empty path inherits that ownership; without
# it the container starts as nonroot against a root-owned mount and dies with
# "unable to open database file", which reads as a corrupt database rather than
# a permission.
RUN mkdir -p /data && chown 65532:65532 /data

# The debug variant, which is the same image plus a busybox shell. Without a
# shell `fly ssh console` cannot open, and on a hosted deployment that is the
# only way to reach the box to mint the first enrolment token — the invite route
# is loopback-only on purpose. A few hundred KB for the one operation that
# cannot be done from outside.
FROM gcr.io/distroless/static-debian12:debug-nonroot
COPY --from=build /comms /comms
COPY --from=build --chown=65532:65532 /data /data
# The log lives on a volume. Without one, a deploy is a factory reset.
VOLUME ["/data"]
EXPOSE 7777
USER nonroot:nonroot
ENTRYPOINT ["/comms", "serve"]
CMD ["-db", "/data/comms.db", "-addr", "0.0.0.0:7777", "-rooms", "core"]
