/* Copyright (C) 2003 Herbert Straub
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, see <https://www.gnu.org/licenses/>.
 */
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <sys/types.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>

#include "Error.h"
#include "ObjLock.h"

ObjLock::ObjLock(const std::string & name)
{
	this->name = name;

	fd = open(name.c_str(), O_RDWR | O_CREAT | O_NONBLOCK,
		  S_IWUSR | S_IRUSR);
	if (fd == -1)
		// (a comma after the SystemError made this throw errno, an
		// int, which nothing catches: the session aborted)
		throw SystemError("Error in ObjLock constructor (open)",
				  errno, ERROR_LOCATION);
#ifndef USE_FLOCK
	memset((void *) &l, 0, sizeof(struct flock));
	l.l_whence = SEEK_SET;
#endif
}

ObjLock::~ObjLock()
{
	// unlock() throws on failure; an exception must not leave a
	// destructor (it would abort the process)
	try {
		unlock();
	}
	catch(...) {
	}
	close(fd);
}

// A lock that another process holds: flock says EWOULDBLOCK; fcntl's
// F_SETLK may say EACCES or EAGAIN (POSIX allows both, as nvflock and
// the active list's refresh lock take them).
static bool held_elsewhere(int e)
{
#ifdef USE_FLOCK
	return e == EWOULDBLOCK;
#else
	return e == EACCES || e == EAGAIN || e == EWOULDBLOCK;
#endif
}

// The blocking calls wait on after EINTR: after SIGTERM the alarm comes
// every second (see catchsignal in NewsCache.cc), and a post that the
// news server didn't take must still reach the spool. The lock is held
// only while one file is stored or removed. They threw SystemError, and
// the article was lost.
#ifdef USE_FLOCK
static int flock_wait(int fd, int op)
{
	int r;
	while ((r = flock(fd, op)) == -1 && errno == EINTR)
		;
	return r;
}
#else
static int setlk_wait(int fd, struct flock *l)
{
	int r;
	while ((r = fcntl(fd, F_SETLKW, l)) == -1 && errno == EINTR)
		;
	return r;
}
#endif

void ObjLock::lockShBlk(void)
{
#ifdef USE_FLOCK
	if (flock_wait(fd, LOCK_SH) == -1)
		throw(SystemError("Error flock", errno));
#else
	l.l_type = F_RDLCK;
	if (setlk_wait(fd, &l) == -1)
		throw(SystemError("Error fcntl", errno));
#endif
}

int ObjLock::lockShNoBlk(void)
{
	int s;

#ifdef USE_FLOCK
	s = flock(fd, LOCK_SH | LOCK_NB);
#else
	l.l_type = F_RDLCK;
	s = fcntl(fd, F_SETLK, &l);
#endif
	if (s == 0) {
		return locked;
	} else if (held_elsewhere(errno)) {
		return not_locked;
	} else {
		throw(SystemError("Error in lockShNoBlk", errno,
				  ERROR_LOCATION));
	}
}

void ObjLock::lockExBlk(void)
{
#ifdef USE_FLOCK
	if (flock_wait(fd, LOCK_EX) == -1)
		throw(SystemError("Error in flock", errno,
				  ERROR_LOCATION));
#else
	l.l_type = F_WRLCK;
	if (setlk_wait(fd, &l) == -1)
		throw(SystemError("Error in fcntl", errno,
				  ERROR_LOCATION));
#endif
}

int ObjLock::lockExNoBlk(void)
{
	int s;

#ifdef USE_FLOCK
	s = flock(fd, LOCK_EX | LOCK_NB);
#else
	l.l_type = F_WRLCK;
	s = fcntl(fd, F_SETLK, &l);
#endif
	if (s == 0) {
		return locked;
	} else if (held_elsewhere(errno)) {
		return not_locked;
	} else {
		throw(SystemError("Error in flock or fcntl", errno,
				  ERROR_LOCATION));
	}
}

void ObjLock::unlock(void)
{
#ifdef USE_FLOCK
	if (flock_wait(fd, LOCK_UN) == -1)
		throw(SystemError("Error in flock", errno,
				  ERROR_LOCATION));
#else
	l.l_type = F_UNLCK;
	if (setlk_wait(fd, &l) == -1)
		throw(SystemError("Error in fcntl", errno,
				  ERROR_LOCATION));
#endif
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
