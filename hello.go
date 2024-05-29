package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
)

type Client struct {
	conn *websocket.Conn
	id   string
	role string
	room string
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var clients = make(map[*websocket.Conn]*Client)
var rooms = make(map[string]map[*websocket.Conn]bool)
var mutex = &sync.Mutex{}
var db *sql.DB

func main() {
	var err error
	connStr := "user=postgres password=Aruzhan7 dbname=amina sslmode=disable"
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Добавляем новый столбец disabled к таблице messages, если его еще нет
	_, err = db.Exec("ALTER TABLE messages ADD COLUMN IF NOT EXISTS disabled BOOLEAN DEFAULT FALSE")
	if err != nil {
		log.Fatal("Error adding disabled column to messages table:", err)
	}

	http.HandleFunc("/echo", handleConnections)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	log.Println("Server started on :8080")
	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Server failed: %s", err)
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	room := r.URL.Query().Get("room")

	if role != "admin" && role != "user1" && role != "user2" && role != "user3" {
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Error upgrading connection:", err)
		return
	}

	clientID := uuid.New().String()
	client := &Client{
		conn: conn,
		id:   clientID,
		role: role,
		room: room,
	}

	mutex.Lock()
	if _, ok := rooms[room]; !ok {
		rooms[room] = make(map[*websocket.Conn]bool)
	}
	rooms[room][conn] = true
	clients[conn] = client
	mutex.Unlock()

	defer func() {
		mutex.Lock()
		delete(rooms[room], conn)
		delete(clients, conn)
		mutex.Unlock()
		conn.Close()
	}()

	// Отправляем клиенту его ID
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ID:"+clientID)); err != nil {
		log.Println("Error sending client ID:", err)
	}

	if deleteChat := r.URL.Query().Get("deleteChat"); deleteChat != "" {
		disableChat(room)
		return
	}

	// Загружаем историю чата
	loadChatHistory(conn, room)

	// Обрабатываем сообщения от клиента
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error reading message:", err)
			break
		}

		sender := clients[conn]
		fmt.Printf("%s (%s) sent: %s\n", conn.RemoteAddr(), sender.role, string(msg))

		saveMessageToDB(sender.id, sender.role, sender.room, string(msg))

		broadcastMessage(sender.room, msg)
	}
}

func broadcastMessage(room string, msg []byte) {
	mutex.Lock()
	defer mutex.Unlock()

	for clientConn := range rooms[room] {
		if err := clientConn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Println("Error writing message:", err)
			clientConn.Close()
			delete(rooms[room], clientConn)
			delete(clients, clientConn)
		}
	}
}

func saveMessageToDB(clientID, role, room, message string) {
	_, err := db.Exec("INSERT INTO messages (client_id, role, room, message) VALUES ($1, $2, $3, $4)", clientID, role, room, message)
	if err != nil {
		log.Println("Error saving message to database:", err)
	}
}

func loadChatHistory(conn *websocket.Conn, room string) {
	rows, err := db.Query("SELECT role, message FROM messages WHERE room = $1 AND disabled = FALSE", room)
	if err != nil {
		log.Println("Error loading chat history:", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var role, message string
		if err := rows.Scan(&role, &message); err != nil {
			log.Println("Error scanning chat history:", err)
			return
		}
		fullMessage := fmt.Sprintf("%s: %s", role, message)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(fullMessage)); err != nil {
			log.Println("Error sending chat history:", err)
			return
		}
	}
}

func disableChat(room string) {
	_, err := db.Exec("UPDATE messages SET disabled = TRUE WHERE room = $1", room)
	if err != nil {
		log.Println("Error disabling chat:", err)
		return
	}
	log.Println("Chat in room", room, "disabled.")
}
