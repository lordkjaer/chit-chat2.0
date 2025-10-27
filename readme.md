How to run the program:
1. In the project root write: go mod tidy (this downloads any missing dependencies).
   
2. Start the server:
In a new terminal do the following
 - cd Server
 - go run server.go
You should now see "Server listening on x"
Keep the terminal open, it will print logs when clients connect, send messages or disconnect.

3. Start multiple clients.
In a new terminal once again do the following
 - cd Client
 - go run client.go <arbitrary-username>
As an example:
"go run client.go Bob"
Each client connect to the same server.

4. Typing messages
- Once both clients are running type a message in one terminal and press "Enter".
- The message will now appear for all clients with a Lamport timestamp.
- All clients will also be able to see which person joins the chit chat room as well as which person leaves the chit chat room.

5. Stopping processes
- To stop any process be it either the server or the clients press Ctrl + C.

