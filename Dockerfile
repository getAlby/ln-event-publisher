FROM golang:1.20-alpine as builder

# Move to working directory /build
WORKDIR /build

# Copy and download dependency using go mod
COPY go.mod .
COPY go.sum .
RUN go mod download

# Copy the code into the container
COPY . .

# Build the application
RUN go build -o main

# Build the utility scripts
RUN go build ./cmd/republish-invoices

# Start a new, final image to reduce size.
FROM alpine as final

# Copy the binaries and entrypoint from the builder image.
COPY --from=builder /build/main /bin/
COPY --from=builder /build/republish-invoices /bin/

ENTRYPOINT [ "/bin/main" ]
