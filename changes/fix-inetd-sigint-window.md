Bug fixes:
* An inetd session no longer waits for another client command when SIGINT or SIGTERM arrives just before its next input read. Its signal-aware input waits atomically and preserves the descriptor flags when stdin and stdout share a socket.
