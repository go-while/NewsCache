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
		errtxt += artSpool;
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
	s = storeArticle(artSpool, a);
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
	s = storeArticle(badArticles, a);
	pLock->unlock();
	if (s != 0) {
		throw(Error("storeBadArt: duplicateArt", ERROR_LOCATION));
	}
}

int ArtSpooler::storeArticle(const string & path, Article & a)
{
	ofstream os;
	string fn, id;
	struct stat st;
	int s;

	id = createID();;
	fn = path + "/art_" + id;
	s = lstat(fn.c_str(), &st);
	if (s == -1) {
		os.open(fn.c_str());
		os << a;
		os.close();
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
