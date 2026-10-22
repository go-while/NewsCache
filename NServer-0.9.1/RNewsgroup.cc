#include "RNewsgroup.h"

// Only remembers the numbers; records are fetched when asked for. (This
// fetched the overview of the whole group, from number 1.)
void RNewsgroup::setsize(artnum_t f, artnum_t l)
{
	if (f != _first || l != _last) {
		if (l < _last)
			slog.p(Logger::Notice) << "watermark(s) decreased?!?\n";
		_OverviewDB.clear();
		_cf = 1;
		_cl = 0;
		_first = f;
		_last = l;
	}
}

// Fetches the records of the chunk that holds nbr, unless they are
// already there. Returns 0 if nbr isn't in the group or fetching failed.
int RNewsgroup::load(artnum_t nbr)
{
	artnum_t a, b;

	if (nbr < _first || nbr > _last || nbr == 0)
		return 0;
	if (_cf <= nbr && nbr <= _cl)
		return 1;
	a = nbr / OVERVIEW_CHUNK * OVERVIEW_CHUNK;
	b = a + OVERVIEW_CHUNK - 1;
	if (a < _first)
		a = _first;
	if (b > _last)
		b = _last;
	_OverviewDB.clear();
	_cf = a;
	_cl = b;
	try {
		_RemoteServer->overviewdb(this, a, b);
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "overview of " << name() << " "
		    << a << "-" << b << " failed: " << e._errtext << "\n";
		_OverviewDB.clear();
		_cf = 1;
		_cl = 0;
		return 0;
	}
	return 1;
}

const char *RNewsgroup::getover(artnum_t nbr)
{
	if (!load(nbr))
		return NULL;
	iterator over = _OverviewDB.find(nbr);
	if (over != _OverviewDB.end())
		return over->second.c_str();
	return NULL;
}

void RNewsgroup::printoverdb(std::ostream & os, artnum_t f, artnum_t l)
{
	artnum_t a;

	if (f < _first)
		f = _first;
	if (l > _last)
		l = _last;
	for (a = f; a <= l && os.good();) {
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i)
				os << i->second << "\r\n";
		}
		// next chunk ("a = _cl + 1" ends at l == ARTNUM_MAX too)
		a = a / OVERVIEW_CHUNK * OVERVIEW_CHUNK + OVERVIEW_CHUNK;
		if (a == 0)
			break;
	}
}

void RNewsgroup::printheaderdb(std::ostream & os, const char *header,
			       artnum_t f, artnum_t l, const char *pat)
{
	std::string xheader = headerfield(header), fld;
	artnum_t a;

	if (f < _first)
		f = _first;
	if (l > _last)
		l = _last;
	for (a = f; a <= l && os.good();) {
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i) {
				try {
					fld = _OverviewFormat->
					    getfield(i->second.c_str(),
						     xheader.c_str(), 0);
				}
				catch(NoSuchFieldError & nsfe) {
					headerline(os, i->first, NULL, pat);
					continue;
				}
				headerline(os, i->first, &fld, pat);
			}
		}
		a = a / OVERVIEW_CHUNK * OVERVIEW_CHUNK + OVERVIEW_CHUNK;
		if (a == 0)
			break;
	}
}

void RNewsgroup::printlistgroup(std::ostream & os, artnum_t f, artnum_t l)
{
	artnum_t a;

	if (f < _first)
		f = _first;
	if (l > _last)
		l = _last;
	if (f > l)
		return;
	try {
		_RemoteServer->printlistgroup(_NewsgroupName, os, f, l,
					      f <= _first && l >= _last);
		return;
	}
	catch(ResponseError & e) {
		// no LISTGROUP (with a range): the overview records tell
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "listgroup " << name() << " failed: "
		    << e._errtext << "\n";
		return;
	}
	for (a = f; a <= l && os.good();) {
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i)
				os << i->first << "\r\n";
		}
		a = a / OVERVIEW_CHUNK * OVERVIEW_CHUNK + OVERVIEW_CHUNK;
		if (a == 0)
			break;
	}
}

void RNewsgroup::printarticle(std::ostream & os, artnum_t nbr)
{
	try {
		Article article;
		_RemoteServer->article(_NewsgroupName, nbr, &article);
		os << article;
	} catch(...) {
	}
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
