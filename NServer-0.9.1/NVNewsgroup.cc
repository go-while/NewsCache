#include<ctype.h>
#include<sys/stat.h>
#include<sys/types.h>
#include<stdio.h>
#include<utime.h>
#include<unistd.h>
#include<string.h>
#include<strings.h>
#include<errno.h>

#include<vector>

#include"Logger.h"
#include"Error.h"
#include"NVNewsgroup.h"
#include"History.h"

using namespace std;

/* Structure of a record of the NVArray
 * 4 Bytes ... Size of record (s)
 * s Bytes ... The record itself
 *
 * Structure of a record
 * 1 Byte ... Type of information (overview,article,bigarticle)
 * n Bytes ... Article/Overviewrecord, including trailing \0
 */

#ifdef ENABLE_ASSERTIONS
void NVNewsgroup::testdb(void)
{
	//  VERB(slog.p(Logger::Debug) << "NVNewsgroup::testdb()\n"); 
	char *data;
	size_t szdata;
	unsigned long f, i, l;
//   Article *art=NULL;
	int corr = 0;

	NVArray::lock(NVcontainer::ShrdLock);
	NVArray::getsize(&f, &l);
	for (i = f; i <= l; i++) {
		NVArray::sget(i, &data, &szdata);
		if (data && *data != overview && *data != article
		    && *data != bigarticle) {
			corr++;
		}
	}
	if (corr) {
		slog.
		    p(Logger::
		      Alert) << "NVNewsgroup::testdb: Oops, " << corr <<
		    " errors found in database!\n";
	}
	NVArray::lock(NVcontainer::UnLock);
}
#endif

void NVNewsgroup::sprintover(ostream & os, artnum_t nbr)
{
	char *data;
	size_t szdata;
	unsigned long f, l;
	string over;

	NVArray::getsize(&f, &l);
	if (f <= nbr && nbr <= l) {
		NVArray::sget(nbr, &data, &szdata);
		if (data) {
			if (*data == article) {
				Article *art;
				//FIX! This is inefficient!
				//FIX! The article is copied and all the overview data is copied 
				//FIX! again. We should allow Articles to use externally provided 
				//FIX! memory (eg. memory from the NVNewsgroup).
				// szdata ... Size of article+1 including the trailing \0
				art =
				    new Article(nbr, data + sizeof(char),
						szdata - sizeof(char) - 1);
				try {
					_OverviewFormat->convert(*art, over);
				}
				catch(const InvalidArticleError & e) {
					delete art;
					corrupt("stored article unreadable");
				}
				os << over << "\r\n";
				delete art;
			} else if (*data == bigarticle
				   || *data == overview) {
				os << data + sizeof(char) << "\r\n";
			} else {
				// Oops, inconsistent
				VERB(slog.
				     p(Logger::
				       Alert) <<
				     "NVN::sprintover(...): inconsistent newsgroup database!\n");
			}
		}
	}
}

#define SYNC_MAGIC 0x31434e5953434e4eULL	/* "NNCSYNC1" */

static inline artnum_t chunk_start(artnum_t i)
{
	return i / OVERVIEW_CHUNK * OVERVIEW_CHUNK;
}

NVNewsgroup::SyncHdr *NVNewsgroup::synchdr(void)
{
	size_t sz;
	SyncHdr *h = (SyncHdr *) sextra(&sz);

	if (!h || sz < sizeof(SyncHdr) || h->magic != SYNC_MAGIC ||
	    h->chunk != OVERVIEW_CHUNK || h->first != arrfst / OVERVIEW_CHUNK ||
	    h->n != arrlst / OVERVIEW_CHUNK - arrfst / OVERVIEW_CHUNK + 1 ||
	    sz < sizeof(SyncHdr) + h->n * sizeof(SyncEnt))
		return NULL;
	return h;
}

// The synchronization entry of chunk c (numbers c*OVERVIEW_CHUNK...);
// returns 0 and a zero entry if there is none.
int NVNewsgroup::getsync(artnum_t c, SyncEnt * e)
{
	SyncHdr *h = synchdr();

	if (h && h->first <= c && c - h->first < h->n) {
		*e = syncents(h)[c - h->first];
		return 1;
	}
	e->time = e->through = 0;
	return 0;
}

void NVNewsgroup::setsync(artnum_t c, nvtime_t time, artnum_t through)
{
	SyncHdr *h = synchdr();

	if (!h && arrfst <= arrlst) {
		// an index of an older version: add the entries
		resize_index(arrfst, arrlst);
		h = synchdr();
	}
	if (h && h->first <= c && c - h->first < h->n) {
		SyncEnt *e = &syncents(h)[c - h->first];
		e->time = time;
		e->through = through;
	}
}

// The news server's last article number when the index was last
// updated (older versions: the end of the index), 0 if unknown.
artnum_t NVNewsgroup::knownlast(void)
{
	SyncHdr *h = synchdr();

	if (h && h->glast)
		return h->glast;
	return arrfst <= arrlst ? arrlst : 0;
}

void NVNewsgroup::setknownlast(artnum_t l)
{
	SyncHdr *h = synchdr();

	if (!h) {
		if (arrfst > arrlst)
			return;
		resize_index(arrfst, arrlst);
		if (!(h = synchdr()))
			return;
	}
	h->glast = l;
}

// The Message-ID of the record data (type byte first), "" if none.
std::string NVNewsgroup::recordmsgid(const char *data, size_t szdata)
{
	try {
		if (*data == article) {
			Article art(0, data + sizeof(char),
				    szdata - sizeof(char) - 1);
			return art.getfield("Message-ID:");
		}
		if (*data == bigarticle || *data == overview)
			return _OverviewFormat->getfield(data + sizeof(char),
							 "Message-ID:", 0);
	}
	catch(...) {
	}
	return "";
}

// Frees the records of [f,l] in the index, and the .art files of big
// articles; their entries go from the history. Needs the exclusive lock.
void NVNewsgroup::freerange(unsigned long f, unsigned long l)
{
	char fn[MAXPATHLEN];
	unsigned long i;

	if (arrfst > arrlst)
		return;
	if (f < arrfst)
		f = arrfst;
	if (l > arrlst)
		l = arrlst;
	HistoryBatch hb;
	for (i = f; i <= l; i++) {
		nvoff_t *slot = &arrtab[i - arrfst];
		if (*slot) {
			char *data;
			size_t szdata;

			NVArray::sget(i, &data, &szdata);	// (checks the record)
			if (History::enabled()) {
				std::string id = recordmsgid(data, szdata);
				if (!id.empty())
					History::remove(id.c_str(), name());
			}
			if (*data == bigarticle) {
				artfile(fn, sizeof(fn), i);
				unlink(fn);
			}
			nvfree(*slot);
			*slot = 0;
		}
		if (i == l)
			break;
	}
}

// Sets the index to [f,l] (f <= l): drops the records outside, keeps
// the ones inside and the synchronization entries of their chunks.
// Needs the exclusive lock.
void NVNewsgroup::resize_index(unsigned long f, unsigned long l)
{
	VERB(slog.p(Logger::Debug) << "NVNewsgroup::resize_index(" << f
	     << "," << l << ")\n");
	uint64_t first = f / OVERVIEW_CHUNK;
	uint64_t n = l / OVERVIEW_CHUNK - first + 1, k;
	artnum_t glast = knownlast();
	std::vector < SyncEnt > ents(n);
	SyncHdr *h;
	size_t sz;

	for (k = 0; k < n; k++)
		ents[k].time = ents[k].through = 0;
	if ((h = synchdr()) != NULL) {
		SyncEnt *e = syncents(h);
		for (k = 0; k < h->n; k++) {
			uint64_t c = h->first + k;
			if (first <= c && c - first < n)
				ents[c - first] = e[k];
		}
	}
	if (arrfst <= arrlst) {
		if (arrfst < f)
			freerange(arrfst, f - 1);
		if (l < arrlst)
			freerange(l + 1, arrlst);
	}
	// (the memory may move, h is invalid from here)
	NVArray::ssetsize(f, l, sizeof(SyncHdr) + n * sizeof(SyncEnt));
	h = (SyncHdr *) sextra(&sz);
	h->magic = SYNC_MAGIC;
	h->chunk = OVERVIEW_CHUNK;
	h->first = first;
	h->n = n;
	h->glast = glast;
	memcpy(syncents(h), &ents[0], n * sizeof(SyncEnt));
}

// Drops every record and synchronization entry, keeps the index range.
void NVNewsgroup::clearindex(void)
{
	SyncHdr *h;

	if (arrfst > arrlst)
		return;
	freerange(arrfst, arrlst);
	if ((h = synchdr()) != NULL)
		memset(syncents(h), 0, h->n * sizeof(SyncEnt));
}

// Limits [f,l] to the group's article numbers; 0 if nothing is left.
int NVNewsgroup::clamp(artnum_t * f, artnum_t * l)
{
	artnum_t gf, gl;

	getsize(&gf, &gl);
	if (*f < gf)
		*f = gf;
	if (*f == 0)
		*f = 1;
	if (*l > gl)
		*l = gl;
	return *f <= *l;
}

/*
 * Makes the index cover as much of [f,l] (already clamped) as it may:
 * at most MAX_INDEX_SPAN numbers, the newest ones. Returns the first
 * covered number (everything from there to l is covered), or 0 if no
 * part is: numbers below the index that are far from it are served
 * from the news server without caching, rather than growing the index
 * across a large gap.
 */
artnum_t NVNewsgroup::cover(artnum_t f, artnum_t l)
{
	artnum_t want = f, gf = _ghave ? _gfirst : 0, r;
	unsigned long lo, hi;

#if ULONG_MAX < 0xffffffffffffffffULL
	// The spool's index holds unsigned longs, 32 bits here.
	if (l > ULONG_MAX)
		return 0;
#endif
	if (l - f + 1 > MAX_INDEX_SPAN)
		want = l - MAX_INDEX_SPAN + 1;

	NVLock guard(*this, NVcontainer::ExclLock);	// (resize_index allocates)
	if (arrfst > arrlst || arrlst < gf || (_ghave && arrfst > _glast)) {
		// no index yet, or none with anything of the group in it
		lo = chunk_start(want);
		hi = l;
	} else {
		artnum_t span = arrlst - arrfst + 1, gap, need;

		lo = arrfst;
		hi = arrlst;
		if (l > hi) {
			gap = want > hi ? want - hi - 1 : 0;
			need = l - want + 1;
			if (gap > span && gap > need && gap > OVERVIEW_CHUNK) {
				// new articles far above the cached ones: start
				// over with the new ones, the old ones go
				lo = chunk_start(want);
			}
			hi = l;
		}
		if (want < lo) {
			gap = l < lo ? lo - l - 1 : 0;
			need = l - want + 1;
			if (gap > span && gap > need && gap > OVERVIEW_CHUNK) {
				// far below the cached part: not cached
				return 0;
			}
			// grow downwards by at least the current size, so that
			// reading backwards doesn't copy the index each time
			artnum_t nlo = want;
			span = hi - lo + 1;
			if (lo > span && lo - span < nlo)
				nlo = lo - span;
			lo = chunk_start(nlo);
		}
	}
	if (lo < gf)
		lo = gf;
	if (hi - lo + 1 > MAX_INDEX_SPAN)
		lo = chunk_start(hi - MAX_INDEX_SPAN + OVERVIEW_CHUNK);
	if (lo != arrfst || hi != arrlst || !synchdr())
		resize_index(lo, hi);
	r = 0;
	if (arrfst <= l && l <= arrlst)
		r = f < arrfst ? arrfst : f;
	return r;
}

/*
 * The group's range on the news server is [f,l]. Records below f are
 * dropped (expired), and above l (renumbered). The index doesn't grow
 * here; cover does that when a client asks for articles.
 */
void NVNewsgroup::setsize(artnum_t f, artnum_t l)
{
	VERB(slog.
	     p(Logger::
	       Debug) << "NVNewsgroup::setsize(" << f << "," << l <<
	     ")\n");
	unsigned long lo, hi;

	_gfirst = f;
	_glast = l;
	_ghave = 1;

	NVLock guard(*this, NVcontainer::ExclLock);	// (resize_index allocates)
	if (arrfst <= arrlst) {
		lo = arrfst;
		hi = arrlst;
		if (f > lo)
			lo = f > ULONG_MAX ? ULONG_MAX : f;
		if (l < hi)
			hi = l;
		if (lo > hi || f > l) {
			// nothing of the group in the index any more
			clearindex();
		} else {
			if (hi - lo + 1 > MAX_INDEX_SPAN) {
				// an index of an older version, which covered
				// the whole group: keep the newest part
				lo = chunk_start(hi - MAX_INDEX_SPAN +
						 OVERVIEW_CHUNK);
			}
			if (lo != arrfst || hi != arrlst)
				resize_index(lo, hi);
		}
	}
}

bool NVNewsgroup::spooled(artnum_t nbr)
{
	char *data;
	size_t szdata;
	unsigned long f, l;
	bool r = false;

	NVArray::lock(NVcontainer::ShrdLock);
	try {
		NVArray::getsize(&f, &l);
		if (nbr >= f && nbr <= l) {
			NVArray::sget(nbr, &data, &szdata);
			if (data && *data == article) {
				r = true;
			} else if (data && *data == bigarticle) {
				char fn[MAXPATHLEN];
				struct stat st;
				artfile(fn, sizeof(fn), nbr);
				r = stat(fn, &st) == 0;
			}
		}
	}
	catch(...) {
		NVArray::lock(NVcontainer::UnLock);
		throw;
	}
	NVArray::lock(NVcontainer::UnLock);
	return r;
}

Article *NVNewsgroup::getarticle(artnum_t nbr)
{
	VERB(slog.
	     p(Logger::
	       Debug) << "NVNewsgroup::getarticle(" << nbr << ")\n");
	char *data;
	size_t szdata;
	unsigned long f, l;
	Article *art = NULL;
	bool from_spool = false;

	// (retrievearticle may throw: the guard gives the lock back)
	NVLock guard(*this, NVcontainer::ShrdLock);
	NVArray::getsize(&f, &l);
	if (nbr >= f && nbr <= l) {
		NVArray::sget(nbr, &data, &szdata);
		if (data) {
			if (*data == article) {
				from_spool = true;
				// szdata ... Size of article including type and the trailing \0
				art =
				    new Article(nbr, data + sizeof(char),
						szdata - sizeof(char) - 1);
				// Every stored article has a Message-ID; a record
				// without one, or that doesn't parse, is damaged.
				try {
					art->getfield("Message-ID:");
				}
				catch(const Error & e) {
					delete art;
					art = NULL;
					corrupt("stored article unreadable");
				}
			} else if (*data == bigarticle) {
				// The article's own file. One that can't be
				// opened (gone, or no file descriptor left) or
				// that is empty or has no Message-ID (a write
				// that failed in an older version, kept on a
				// full disk) is not served: the article comes
				// from the news server, and a bad file goes.
				char fn[MAXPATHLEN];
				ifstream fs;
				artfile(fn, sizeof(fn), nbr);
				fs.open(fn);
				if (fs.good()) {
					Article *a = new Article(nbr);
					bool ok = false;
					try {
						a->read(fs);
						ok = a->length() > 0 &&
						    !a->getfield("Message-ID:").empty();
					}
					catch(const Error & e) {
					}
					if (ok) {
						art = a;
						from_spool = true;
					} else {
						delete a;
						slog.p(Logger::Warning) << fn
						    << ": empty or unreadable, fetching the article again\n";
						unlink(fn);
					}
				}
				fs.close();
				if (!art && (art = retrievearticle(nbr)) != NULL)
					storearticle(art);
			} else {
				// Article not available
				if ((art = retrievearticle(nbr)) != NULL)
					storearticle(art);
			}
		} else {
			// Article not available
			if ((art = retrievearticle(nbr)) != NULL)
				storearticle(art);
		}
	}
	guard.release();
	if (art && from_spool && History::enabled()) {
		// a spool of an older version has no history entries
		try {
			History::note(art->getfield("Message-ID:").c_str(),
				      name(), nbr);
		}
		catch(...) {
		}
	}
	return art;
}

// Stores an article fetched for a client, when there is room: without
// (disk full, ...) the client gets it all the same, and it is fetched
// again next time.
void NVNewsgroup::storearticle(Article * art)
{
	try {
		setarticle(art);
	}
	catch(const SystemError & e) {
		slog.p(Logger::Error) << "cannot store article " << art->getnbr()
		    << " of " << name() << " in the spool\n";
		e.print();
	}
}

void NVNewsgroup::setarticle(Article * art)
{
	unsigned long f, l;
	artnum_t nbr;

	VERB(slog.p(Logger::Debug) << "NVNewsgroup::setarticle(*art(nbr="
	     << art->getnbr() << "))\n");
	ASSERT2(testdb());

	nbr = art->getnbr();
	NVLock guard(*this, NVcontainer::ExclLock);	// (nvalloc may throw)
	NVArray::getsize(&f, &l);
	if (f <= nbr && nbr <= l) {
		char *data;
		size_t szdata;
		unsigned long i;

		i = nbr - arrfst;
		NVArray::sget(nbr, &data, &szdata);
		if (art->length() <= 15360) {
			// Article smaller/equal 15k => Store article in NewsgroupDB
			// (the new block first: without space the old record
			// stays as it is)
			nvoff_t x, old = data ? arrtab[i] : 0;
			szdata = art->length() + 1 + sizeof(char);
			x = nvalloc(szdata + sizeof(unsigned long));
			if (old)
				nvfree(old);
			arrtab[i] = x;
			data = mem_p + x;
			*((unsigned long *) data) = szdata;
			data += sizeof(unsigned long);
			*data = article;
//       VERB(sprintf(buf,"NVN::setarticle: Before memcpy arrtab=%p data=%p\n",arrtab,data);
//         slog.p(Logger::Debug) << buf);
			ASSERT2(testdb());
			memcpy(data + sizeof(char), art->c_str(),
			       art->length() + 1);
		} else if (art->length() > 15360) {
			// Article bigger 15k => Store externally
			// (a stored article's record goes after the new
			// one is allocated, as above)
			nvoff_t old = 0;
			if (data && *data != overview
			    && *data != bigarticle) {
				old = arrtab[i];
				data = NULL;
			}
			if (!data) {
				string over;
				nvoff_t x;
				_OverviewFormat->convert(*art, over);
				szdata = over.length() + 1 + sizeof(char);
				x = nvalloc(szdata +
					    sizeof(unsigned long));
				arrtab[i] = x;
				data = mem_p + x;
				*((unsigned long *) (data)) = szdata;
				data += sizeof(unsigned long);
				memcpy(data + sizeof(char), over.c_str(),
				       over.length() + 1);
				*data = overview;
			}
			if (old)
				nvfree(old);
			char fn[MAXPATHLEN];
			ofstream fs;
			artfile(fn, sizeof(fn), nbr);
			fs.open(fn);
			fs << *art;
			fs.close();
			if (fs.fail()) {
				// (disk full, ...) a cut-off file must not be
				// served: the article stays an overview record
				int e = errno;
				unlink(fn);
				*data = overview;
				throw SystemError(string("cannot write ") + fn,
						  e, ERROR_LOCATION);
			}
			*data = bigarticle;
		} else {
			VERB(slog.
			     p(Logger::
			       Debug) <<
			     "NVNewsgroup::setarticle: Tried to store an existing article\n");
		}
		if (History::enabled()) {
			try {
				History::add(art->getfield("Message-ID:").c_str(),
					     name(), nbr);
			}
			catch(...) {
			}
		}
	}			/* if(f<=nbr<=l) */
	guard.release();
	ASSERT2(testdb());
}

void NVNewsgroup::printarticle(ostream & os, artnum_t nbr)
{
	char *data;
	size_t szdata;
	unsigned long f, l;

	NVArray::lock(NVcontainer::ShrdLock);
	NVArray::getsize(&f, &l);
	if (nbr >= f && nbr <= l) {
		NVArray::sget(nbr, &data, &szdata);
		if (data) {
			if (*data == article) {
				os << data + sizeof(char);
			} else if (*data == bigarticle) {
				Article art(nbr);
				char fn[MAXPATHLEN];
				ifstream fs;
				artfile(fn, sizeof(fn), nbr);
				fs.open(fn);
				art.read(fs);
				fs.close();
				os << art;
			} else {
				// Article not available
				Article *art;
				if ((art = retrievearticle(nbr)) != NULL)
					setarticle(art);
				os << *art;
				delete art;
			}
		}
	}
	NVArray::lock(NVcontainer::UnLock);
}

//FIX! Possibly, we should throw an exception, if the overview record
//FIX! cannot be found.
const char *NVNewsgroup::getover(artnum_t nbr)
{
	//  VERB(slog.p(Logger::Debug) << "NVNewsgroup::getover(over)\n"); 

	char *data;
	size_t szdata;
	unsigned long f, l;
	static string over;

	over = "";
	NVArray::lock(NVcontainer::ShrdLock);
	NVArray::getsize(&f, &l);
	if (f <= nbr && nbr <= l) {
		NVArray::sget(nbr, &data, &szdata);
		if (data) {
			if (*data == article) {
				Article *art;
				//FIX! This is inefficient!
				//FIX! The article is copied and all the overview data is copied 
				//FIX! again. We should allow Articles to use externally provided 
				//FIX! memory (eg. memory from the NVNewsgroup).
				// szdata ... Size of article+1 including the trailing \0
				art =
				    new Article(nbr, data + sizeof(char),
						szdata - sizeof(char) - 1);
				try {
					_OverviewFormat->convert(*art, over);
				}
				catch(const InvalidArticleError & e) {
					delete art;
					corrupt("stored article unreadable");
				}
				delete art;
			} else if (*data == bigarticle
				   || *data == overview) {
				over = data + sizeof(char);
			} else {
				// Impossible
				VERB(slog.
				     p(Logger::
				       Critical) <<
				     "NVNewsgroup::getover. Oops, reached ?dead? code 936!!!\n");
			}
		}
	}
	NVArray::lock(NVcontainer::UnLock);
	return over.c_str();
}

void NVNewsgroup::setover(const string & over)
{
	//  VERB(slog.p(Logger::Notice) << "NVNewsgroup::setover(over)\n"); 

	unsigned long f, l;
	artnum_t nbr;

	nbr = leading_artnum(over.c_str());
	NVLock guard(*this, NVcontainer::ExclLock);	// (nvalloc may throw)
	NVArray::getsize(&f, &l);
	if (f <= nbr && nbr <= l) {
		char *data;
		size_t szdata;
		unsigned long i;

		i = nbr - arrfst;
		NVArray::sget(nbr, &data, &szdata);
		if (!data) {
			szdata = over.length() + 1 + sizeof(char);
			nvoff_t x =
			    nvalloc(szdata + sizeof(unsigned long));
			arrtab[i] = x;
			data = mem_p + x;
			*((unsigned long *) (data)) = szdata;
			data += sizeof(unsigned long);
			memcpy(data + sizeof(char), over.c_str(),
			       over.length() + 1);
			*data = overview;
			if (History::enabled()) {
				std::string id =
				    _OverviewFormat->getfield(over.c_str(),
							      "Message-ID:", 0);
				if (!id.empty())
					History::add(id.c_str(), name(), nbr);
			}
		}
	}
	guard.release();
	ASSERT2(testdb());
}

artnum_t NVNewsgroup::readoverdb(istream & is)
{
	VERB(slog.p(Logger::Debug) << "NVNewsgroup::readoverdb(&is)\n");

	string line1, line2;
	time_t now;
	artnum_t n, high = 0;
	HistoryBatch hb;	// one history lock per shard for the reply

	// No space for a record (disk full): the rest of the reply is read
	// all the same, so the connection to the news server stays in step
	// (the next command read the rest for its reply), and the error is
	// thrown at the end.
	bool failed = false, done = false;
	SystemError err;

	NVLock guard(*this, NVcontainer::ExclLock);
	for (;;) {
		nlreadline(is, line1, 0, NNTP_MAXLINE);
		if (line1 == ".") {
			done = true;
			break;
		}
		if (is.eof())
			break;
		if ((n = leading_artnum(line1.c_str())) > high)
			high = n;
		if (failed)
			continue;
		try {
			if (_OverviewFormat->dotrans) {
				// Convert Record
				_OverviewFormat->convert(line1, line2);
			} else {
				line2 = line1;
			}
			if (_RecordFilter)
				_RecordFilter->filter(line2);
			setover(line2);
		}
		catch(const SystemError & e) {
			failed = true;
			err = e;
		}
	}
	if (failed)
		throw err;

	// (a reply cut off isn't complete: overviewrange throws for it)
	if (done) {
		time(&now);
		setmtime(now);
	}
	return high;
}

void NVNewsgroup::printoverdb(ostream & os, artnum_t f, artnum_t l)
{
	VERB(slog.p(Logger::Debug) << "NVNewsgroup::printoverdb(os,"
	     << f << "," << l << ")\n");
	artnum_t i;
	lock(NVcontainer::ShrdLock);
	if (f < arrfst)
		f = arrfst;
	if (arrlst < l)
		l = arrlst;
	// i == l ends the loop, so l == ARTNUM_MAX can't wrap around
	for (i = f; i <= l; i++) {
		sprintover(os, i);
		if (i == l)
			break;
	}
	lock(NVcontainer::UnLock);
}

void NVNewsgroup::printheaderdb(ostream & os,
				const char *header,
				artnum_t f, artnum_t l, const char *pat)
{
	char *data;
	string fld, xheader = headerfield(header);
	size_t szdata;
	unsigned long gf, i, gl;

	NVArray::lock(NVcontainer::ShrdLock);
	NVArray::getsize(&gf, &gl);
	if (f < gf)
		f = gf;
	if (l > gl)
		l = gl;
	for (i = f; i <= l; i++) {
		NVArray::sget(i, &data, &szdata);
		if (data && (*data == article || *data == bigarticle
			     || *data == overview)) {
			bool found = true;
			if (*data == article) {
				//FIX! This is inefficient!
				//FIX! The article is copied and all the overview data is copied 
				//FIX! again. We should allow Articles to use externally provided 
				//FIX! memory (eg. memory from the NVNewsgroup).
				// szdata ... Size of article including the trailing \0 plus
				//            1char (= InformationTag of NVNewsgroup)
				Article art(i, data + sizeof(char),
					    szdata - sizeof(char) - 1);
				try {
					fld = art.getfield(xheader.c_str(), 0);
				} catch(NoSuchFieldError & nfe) {
					// no Lines header: the body lines,
					// as in the overview (:lines)
					if (strcasecmp(xheader.c_str(),
						       "lines:") == 0) {
						char n[32];
						snprintf(n, sizeof(n), "%lu",
							 art.bodylines());
						fld = n;
					} else {
						found = false;
					}
				} catch(const InvalidArticleError & e) {
					corrupt("stored article unreadable");
				}
			} else {
				const char *over = data + sizeof(char);
				try {
					fld =
					    _OverviewFormat->getfield(over,
								      xheader.c_str(),
								      0);
				} catch(NoSuchFieldError & nfe) {
					found = false;
				}
			}
			headerline(os, i, found ? &fld : NULL, pat);
		}
		if (i == l)
			break;
	}
	NVArray::lock(NVcontainer::UnLock);
}

void NVNewsgroup::printlistgroup(ostream & os, artnum_t f, artnum_t l)
{
	VERB(slog.
	     p(Logger::Debug) << "NVNewsgroup::printlistgroup(&os," << f
	     << "," << l << ")\n");

	artnum_t i;
	lock(NVcontainer::ShrdLock);
	if (f < arrfst)
		f = arrfst;
	if (l > arrlst)
		l = arrlst;
	// i == l ends the loop, so l == ARTNUM_MAX can't wrap around
	for (i = f; i <= l; i++) {
		if (shas_element(i))
			os << i << "\r\n";
		if (i == l)
			break;
	}
	lock(NVcontainer::UnLock);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
