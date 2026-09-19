package notification

import (
	"bufio"
	"context"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSendSMTPMessageRequiresAdvertisedSTARTTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 smtp.test ESMTP\r\n"))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		if strings.HasPrefix(strings.ToUpper(line), "EHLO ") {
			_, _ = conn.Write([]byte("250-smtp.test\r\n250 AUTH PLAIN\r\n"))
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = sendSMTPMessage(ctx, "127.0.0.1", port, "starttls", true, nil, "from@example.com", []string{"to@example.com"}, []byte("Subject: test\r\n\r\ntest\r\n"))
	if err == nil || !strings.Contains(err.Error(), "does not offer required STARTTLS") {
		t.Fatalf("expected required STARTTLS failure, got %v", err)
	}
	<-serverDone
}

func TestSendSMTPMessageRejectsInvalidTLSCertificate(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = sendSMTPMessage(ctx, host, port, "tls", true, nil, "from@example.com", []string{"to@example.com"}, []byte("test"))
	if err == nil || !strings.Contains(err.Error(), "establish SMTP TLS") {
		t.Fatalf("expected TLS certificate failure, got %v", err)
	}
}
