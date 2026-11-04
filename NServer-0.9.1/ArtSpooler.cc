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


#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

#include "Debug.h"
#include "Error.h"
#include "ArtSpooler.h"
#include "util.h"

#ifdef HAVE_SSTREAM
#include <sstream>
#else
#include <strstream>
#endif

using namespace std;

ArtSpooler::ArtSpooler(const string & spoolDir)
{
	VERB(slog.p(Logger::Debug) << "ArtSpooler::ArtSpooler ("
	     << spoolDir << ")\n");

	string tmp;

	this->spoolDir = spoolDir;
	if (mkpdir(spoolDir.c_str(), 0700) == -1) {
		string errtxt("Error creating spoolDirectory");
		errtxt += spoolDir;
		throw(Error(errtxt.c_str(), ERROR_LOCATION));
	}
	artSpool = spoolDir + "/.artSpool";
	if (mkpdir(artSpool.c_str(), 0700) == -1) {
		string errtxt("Error creating spoolDirectory");
		errtxt += artSpool;
		throw(Error(errtxt.c_str(), ERROR_LOCATION));
	}
	badArticles = spoolDir + "/.badArticles";
	if (mkpdir(badArticles.c_str(), 0700) == -1) {
		string errtxt("Error creating spoolDirectory");
		errtxt += badArticles;
		throw(Error(errtxt.c_str(), ERROR_LOCATION));
	}
	tmp = spoolDir + "/.resourceSpooler.lock";
	pLock = new ObjLock(tmp);
	pPostLock = NULL;
}

ArtSpooler::~ArtSpooler()
{
	VERB(slog.p(Logger::Debug) << "ArtSpooler::~ArtSpooler ()\n");

	delete pLock;
	delete pPostLock;
}

void ArtSpooler::spoolArt(Article & a)
{
	VERB(slog.
	     p(Logger::Debug) << "ArtSpooler::spoolArt (Article &)\n");
	int s;

	pLock->lockExBlk();
	try {
		s = storeArticle(artSpool, a);
	} catch(...) {
		pLock->unlock();
		throw;
	}
	pLock->unlock();

	if (s != 0) {
		throw(Error("spoolArt: duplicateArt", ERROR_LOCATION));
	}

}

void ArtSpooler::storeBadArt(Article & a)
{
	VERB(slog.
	     p(Logger::Debug) << "ArtSpooler::storeBadArt (Article &)\n");
	int s;

	pLock->lockExBlk();
	try {
		s = storeArticle(badArticles, a);
	} catch(...) {
		pLock->unlock();
		throw;
	}
	pLock->unlock();
	if (s != 0) {
		throw(Error("storeBadArt: duplicateArt", ERROR_LOCATION));
	}
}

// The article is written under a temporary name, made durable (fsync) and
// renamed to art_*, and the directory is synced; only then does the
// caller tell the client "240 Article posted". getSpooledArt never takes
// a half-written file, and after a power cut art_* is not a zero-length
// file (a newly named file is not flushed by the rename alone). A failed
// open or write (a full disk, a file size limit) throws SystemError and
// leaves nothing behind: the client was told "240 Article posted", and a
// cut-off article was posted later.
int ArtSpooler::storeArticle(const string & path, Article & a)
{
	ostringstream os;
	string fn, tmp, id, text;
	struct stat st;
	int s, e = 0, fd;

	id = createID();;
	fn = path + "/art_" + id;
	tmp = path + "/tmp_" + id;
	s = lstat(fn.c_str(), &st);
	if (s == -1) {
		os << a;
		text = os.str();
		fd = ::open(tmp.c_str(), O_WRONLY | O_CREAT | O_EXCL, 0644);
		if (fd < 0) {
			e = errno;
		} else {
			const char *p = text.data();
			size_t left = text.length();
			while (left > 0) {
				ssize_t n = ::write(fd, p, left);
				if (n < 0) {
					if (errno == EINTR)
						continue;
					e = errno;
					break;
				}
				p += n;
				left -= n;
			}
			if (!e && fsync(fd) != 0)
				e = errno;
			if (::close(fd) != 0 && !e && errno != EINTR)
				e = errno;
			if (!e && rename(tmp.c_str(), fn.c_str()) != 0)
				e = errno;
		}
		// the new name on the disk too. Where a directory can't be
		// synced (EINVAL, EBADF for a read-only descriptor,
		// ENOTSUP), there is no other way.
		if (!e) {
			int dfd = ::open(path.c_str(), O_RDONLY);
			if (dfd < 0)
				e = errno;
			else if (fsync(dfd) != 0 && errno != EINVAL &&
				 errno != EBADF
#ifdef ENOTSUP
				 && errno != ENOTSUP
#endif
#ifdef EOPNOTSUPP
				 && errno != EOPNOTSUPP
#endif
				 )
				e = errno;
			if (dfd >= 0)
				::close(dfd);
			if (e)
				unlink(fn.c_str());
		} else if (fd >= 0) {
			unlink(tmp.c_str());
		}
		if (e)
			throw SystemError("cannot store article in " + path, e,
					  ERROR_LOCATION);
		s = 0;
	} else {
		s = 1;
	}
	return (s);
}

Article *ArtSpooler::getSpooledArt(void)
{
	VERB(slog.p(Logger::Debug) << "ArtSpooler::getSpooledArt ()\n");
	string an;
	Article *pa;

	pa = nextSpooledArt(an);
	if (pa)
		removeSpooledArt(an);
	return pa;
}

// The file stays until removeSpooledArt: getSpooledArt removed it before
// the article was posted, so it was lost when updatenews was killed or
// crashed in between.
Article *ArtSpooler::nextSpooledArt(string & name)
{
	VERB(slog.p(Logger::Debug) << "ArtSpooler::nextSpooledArt ()\n");
	string an;
	Article *pa = NULL;
	ifstream is;
	struct dirent *d;
	DIR *dir;
	int e;

	pLock->lockExBlk();
	dir = opendir(artSpool.c_str());
	if (dir == NULL) {
		e = errno;
		pLock->unlock();
		throw SystemError("cannot open " + artSpool, e, ERROR_LOCATION);
	}
	for (d = readdir(dir); d != NULL; d = readdir(dir)) {
		// A tmp_* file is left by a process that was killed or lost
		// power while it stored an article. Every writer holds this
		// lock, so none is being written now, and nothing else
		// would ever remove it (newscacheclean skips .artSpool).
		if (strncmp("tmp_", d->d_name, 4) == 0) {
			an = artSpool + "/" + d->d_name;
			unlink(an.c_str());
			continue;
		}
		if (strncmp("art_", d->d_name, 4) == 0) {
			an = artSpool + "/" + d->d_name;
			is.open(an.c_str());
			if (!is.is_open()) {
				slog.p(Logger::Error) << "cannot open the spooled article "
				    << an << ": " << strerror(errno) << "\n";
				continue;
			}
			pa = new Article();
			pa->read(is);
			is.close();
			name = an;
			break;
		}
	}
	closedir(dir);
	pLock->unlock();

	return pa;
}

void ArtSpooler::removeSpooledArt(const string & name)
{
	int e;

	pLock->lockExBlk();
	if (unlink(name.c_str()) != 0 && errno != ENOENT) {
		e = errno;
		pLock->unlock();
		throw SystemError("cannot remove " + name, e, ERROR_LOCATION);
	}
	pLock->unlock();
}

bool ArtSpooler::lockPosting(void)
{
	if (!pPostLock)
		pPostLock = new ObjLock(spoolDir + "/.postSpooler.lock");
	return pPostLock->lockExNoBlk() == ObjLock::locked;
}

void ArtSpooler::unlockPosting(void)
{
	pPostLock->unlock();
}

string ArtSpooler::createID(void)
{
#ifdef HAVE_SSTREAM
	stringstream sb;
#else
	strstream sb;
#endif
	pid_t pid;
	struct timeval t;

	pid = getpid();
	gettimeofday(&t, NULL);
	sb << (int) pid << (int) t.tv_sec << (int) t.tv_usec;
	return sb.rdbuf()->str();
}

string ArtSpooler::extractID(Article & a)
{
	string ID = a.getfield("message-id");
	ID.replace(0, 3, "");
	ID.replace(ID.length() - 1, 1, "");
	return ID;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
