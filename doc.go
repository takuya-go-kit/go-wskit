// Package wskit provides a WebSocket hub-and-spoke server built on coder/websocket.
//
// # Hub and Client
//
// Create a Hub with NewHub (optionally WithRedis for multi-instance broadcast), run Hub.Run(ctx) in a goroutine,
// then use Accept to upgrade HTTP connections and register clients. Run Client.ReadPump and Client.WritePump in separate goroutines per connection.
//
// # Event envelope
//
// Event and NewEvent provide a standard JSON envelope (type, payload, timestamp). Use Hub.BroadcastEvent or Hub.BroadcastJSON to send to all clients.
//
// # Redis Pub/Sub
//
// WithRedis(client, channel) enables publishing to Redis on BroadcastEvent/BroadcastJSON; other instances run SubscribeToRedis(ctx) to receive and broadcast locally.
//
// # Options
//
// Hub: WithRedis, WithBroadcastBuf, WithRegisterBuf, WithChannelTimeout, WithOnTimeout, WithOnConnect.
// Client: WithWriteWait, WithPingInterval, WithMaxMessageSize, WithSendBufSize.
package wskit
