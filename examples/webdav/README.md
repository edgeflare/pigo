# Go WebDAV Server

This is a simple Go WebDAV server implementation using the `golang.org/x/net/webdav` package. The `edgeflare/pigo/middleware` package is used to provide middleware for request ID, CORS, and logging.

## Features

* Serves files from the `./webdav-dir` directory.
* Supports basic WebDAV operations (see below).
* Includes middleware for request ID generation, CORS handling, and logging.

## Common WebDAV Methods

| Method | Description | Example |
|---|---|---|
| `PROPFIND` | Retrieves properties of a resource or collection. | `curl -X PROPFIND -H "Depth: 1" http://localhost:8080/webdav/` |
| `GET` | Retrieves the content of a file. | `curl http://localhost:8080/webdav/filename.txt` |
| `PUT` | Uploads or modifies a file. | `echo 'helloworld' > newfile.txt && curl -T newfile.txt http://localhost:8080/webdav/newfile.txt` |
| `MKCOL` | Creates a new collection (directory). | `curl -X MKCOL http://localhost:8080/webdav/newdirectory/` |
| `DELETE` | Deletes a file or collection. | `curl -X DELETE http://localhost:8080/webdav/file-to-delete.txt` |
| `MOVE` | Moves or renames a file or collection. | `curl -X MOVE -H "Destination: http://localhost:8080/webdav/newname.txt" http://localhost:8080/webdav/oldname.txt` |
| `COPY` | Copies a file or collection. | `curl -X COPY -H "Destination: http://localhost:8080/webdav/copy-of-file.txt" http://localhost:8080/webdav/original-file.txt` |
| `LOCK` | Obtains a lock on a resource. | `curl -X LOCK -H "Timeout: Infinite" http://localhost:8080/webdav/file-to-lock.txt` |
| `UNLOCK` | Releases a lock on a resource. | `curl -X UNLOCK -H "Lock-Token: <lock-token>" http://localhost:8080/webdav/file-to-unlock.txt` |

**Note:** Replace placeholders like `<lock-token>` with actual values.

## Running the Server

1. Make sure you have Go installed.
2. Install the required dependencies: `go get golang.org/x/net/webdav github.com/edgeflare/pigo/middleware`
3. Create a directory named `webdav-dir` to store your files.
4. Run the server: `go run main.go`
5. The server will start listening on `http://localhost:8080`.

## Additional Notes

* This server uses a simple in-memory lock system. For production use, consider a more persistent lock manager.
* Refer to the `golang.org/x/net/webdav` documentation for more advanced configuration and features.
