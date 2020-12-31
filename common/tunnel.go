package common

import (
	"fmt"
	"net"

	cs "github.com/hotnops/gTunnel/grpc/client"
)

type ConnectionStreamHandler interface {
	GetByteStream(ctrlMessage *cs.TunnelControlMessage) ByteStream
	CloseStream(connId int32)
	Acknowledge(ctrlMessage *cs.TunnelControlMessage) ByteStream
}

type Tunnel struct {
	id                string
	direction         uint32
	listenIP          net.IP
	listenPort        uint32
	destinationIP     net.IP
	destinationPort   uint32
	connections       map[int32]*Connection
	listeners         []net.Listener
	Kill              chan bool
	ctrlStream        TunnelControlStream
	connectionCount   int32
	ConnectionHandler ConnectionStreamHandler
}

// NewTunnel is a constructor for the tunnel struct. It takes
// in id, direction, listenIP, lisetnPort, destinationIP, and
// destinationPort as parameters.
func NewTunnel(id string,
	direction uint32,
	listenIP net.IP,
	listenPort uint32,
	destinationIP net.IP,
	destinationPort uint32) *Tunnel {
	t := new(Tunnel)
	t.id = id
	t.direction = direction
	t.listenIP = listenIP
	t.listenPort = listenPort
	t.destinationIP = destinationIP
	t.destinationPort = destinationPort
	t.connections = make(map[int32]*Connection)
	t.Kill = make(chan bool)
	t.listeners = make([]net.Listener, 0)
	return t
}

// Addconnection will generate an ID for the connection and
// add it to the map.
func (t *Tunnel) AddConnection(c *Connection) {
	c.Id = t.connectionCount
	t.connections[t.connectionCount] = c
	t.connectionCount += 1
}

// AddListener will start a tcp listener on a specific port and forward
// all accepted TCP connections to the associated tunnel.
func (t *Tunnel) AddListener(listenPort int32, endpointId string) bool {

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", listenPort))
	if err != nil {
		return false
	}

	t.listeners = append(t.listeners, ln)

	newConns := make(chan net.Conn)

	go func(l net.Listener) {
		for {
			c, err := l.Accept()
			if err == nil {
				newConns <- c
			} else {
				return
			}
		}
	}(ln)
	go func() {
		for {
			select {
			case conn := <-newConns:
				gConn := NewConnection(conn)
				t.AddConnection(gConn)
				newMessage := new(cs.TunnelControlMessage)
				newMessage.EndpointId = endpointId
				newMessage.Operation = TunnelCtrlConnect
				newMessage.TunnelId = t.id
				newMessage.ConnectionId = gConn.Id
				t.ctrlStream.Send(newMessage)

			case <-t.Kill:
				return
			}
		}
	}()
	return true
}

// GetConnection will return a Connection object
// with the given connection id
func (t *Tunnel) GetConnection(connID int32) *Connection {
	if conn, ok := t.connections[connID]; ok {
		return conn
	}
	return nil
}

// GetControlStream will return the control stream for
// the associated tunnel
func (t *Tunnel) GetControlStream() TunnelControlStream {
	return t.ctrlStream
}

// GetDestinationIP gets the destination IP of the tunnel.
func (t *Tunnel) GetDestinationIP() net.IP {
	return t.destinationIP
}

// GetDestinationPort gets the destination port of the tunnel.
func (t *Tunnel) GetDestinationPort() uint32 {
	return t.destinationPort
}

// GetDirection gets the direction of the tunnel (forward or reverse)
func (t *Tunnel) GetDirection() uint32 {
	return t.direction
}

// GetListenIP gets the ip address that the tunnel is listening on.
func (t *Tunnel) GetListenIP() net.IP {
	return t.listenIP
}

// GetListenPort gets the port that the tunnel is listening on.
func (t *Tunnel) GetListenPort() uint32 {
	return t.listenPort
}

// GetConnections will return the connection map
func (t *Tunnel) GetConnections() map[int32]*Connection {
	return t.connections
}

// handleIngressCtrlMessages is the loop function responsible
// for receiving control messages from the gRPC stream.
func (t *Tunnel) handleIngressCtrlMessages() {
	ingressMessages := make(chan *cs.TunnelControlMessage)
	go func(s TunnelControlStream) {
		for {
			ingressMessage, err := t.ctrlStream.Recv()
			if err != nil {
				close(ingressMessages)
				return
			}
			ingressMessages <- ingressMessage
		}
	}(t.ctrlStream)
	for {
		select {
		case ctrlMessage, ok := <-ingressMessages:
			if !ok {
				ingressMessages = nil
				break
			}
			if ctrlMessage == nil {
				ingressMessages = nil
				break
			}
			// handle control message
			if ctrlMessage.Operation == TunnelCtrlConnect {

				conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d",
					t.destinationIP,
					t.destinationPort))

				if err != nil {
					ctrlMessage.ErrorStatus = 1
				} else {
					var gConn *Connection
					if gConn, ok = t.connections[ctrlMessage.ConnectionId]; !ok {
						gConn = NewConnection(conn)
						t.connections[ctrlMessage.ConnectionId] = gConn
					}
					stream := t.ConnectionHandler.GetByteStream(ctrlMessage)
					gConn.SetStream(stream)
					gConn.Start()

				}

			} else if ctrlMessage.Operation == TunnelCtrlAck {
				//conn := t.connections[ctrlMessage.ConnectionID]
				if ctrlMessage.ErrorStatus != 0 {
					t.RemoveConnection(ctrlMessage.ConnectionId)
				} else {
					// Now that we know we are connected, we need to create a new byte
					// stream and create a thread to service it
					// If this is client side, we need to still create the byte stream
					conn, ok := t.connections[ctrlMessage.ConnectionId]
					// Waiting until the byte stream gets set up
					conn.SetStream(t.ConnectionHandler.Acknowledge(ctrlMessage))
					if ok {
						conn.Start()
					}
				}
			} else if ctrlMessage.Operation == TunnelCtrlDisconnect {
				t.RemoveConnection(ctrlMessage.ConnectionId)
			}
		case <-t.Kill:
			break
		}
		if ingressMessages == nil {
			break
		}
	}
}

// RemoveConnection will remove the Connection object
// from the connections map.
func (t *Tunnel) RemoveConnection(connID int32) {
	delete(t.connections, connID)
}

// SetControlStream will set the provided control stream for
// the associated tunnel
func (t *Tunnel) SetControlStream(s TunnelControlStream) {
	t.ctrlStream = s
}

// Start receiving control messages for the tunnel
func (t *Tunnel) Start() {
	// A thread for handling the established tcp connections
	go t.handleIngressCtrlMessages()

}

// Stop will stop all associated goroutines for the tunnel
// and disconnect any associated TCP connections
func (t *Tunnel) Stop() {
	// First, stop all the listeners
	for _, ln := range t.listeners {
		ln.Close()
	}

	// Close all existing tcp connections
	for _, conn := range t.connections {
		conn.Close()
	}
	// Lastly, signal that the tunnel stream should be killed
	close(t.Kill)
}
