#include <signal.h>
#include <string.h>
#include <unistd.h>

static volatile sig_atomic_t stopping;

static void stop(int signal_number)
{
	(void) signal_number;
	stopping = 1;
}

int main(int argc, char **argv)
{
	struct sigaction action;

	if (argc == 2 && strcmp(argv[1], "-p") == 0)
		return 0;
	if (argc != 2 || strcmp(argv[1], "-n") != 0)
		return 2;

	memset(&action, 0, sizeof(action));
	action.sa_handler = stop;
	sigemptyset(&action.sa_mask);
	if (sigaction(SIGTERM, &action, 0) < 0)
		return 1;
	action.sa_handler = SIG_IGN;
	if (sigaction(SIGHUP, &action, 0) < 0)
		return 1;
	while (!stopping)
		pause();
	return 0;
}
