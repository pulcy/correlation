# Correlation

Correlation is a smart folder synchronizer.
It uses syncthing to synchronize files and ETCD to find others that want to synchronize the same folder.

## Usage

| Flags            | Description     |
|------------------|-----------------|
| --log-level      | Minimum log level (debug|info|warning|error) |
| --etcd-addr      | Address of etcd backend (e.g. "http://localhost:4001/pulcy/correlation/myfolder") |
| --sync-port      | Port number used by syncthing to synchronize on |
| --http-port      | Port number used by syncthing to listen for GUI & REST API on |
| --announce-ip    | IP address to announce that we're running on |
| --announce-port  | Port number to announce that syncthing is listening on (defaults to --sync-port) |
| --syncthing-path | Full path of syncthing (defaults to /app/syncthing) |
| --sync-dir       | Full path of the folder to synchronize |
| --config-dir     | Full path of the folder to store the synchronization database & config files in |
| --gui-user       | Username used to access the syncthing GUI |
| --gui-password   | Password used to access the syncthing GUI |
