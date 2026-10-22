#include "RNewsgroup.h"

void RNewsgroup::setsize(artnum_t f, artnum_t l)
{
	if (f < _first || l < _last) {
		slog.p(Logger::Notice) << "watermark(s) decreased?!?\n";
		_OverviewDB.erase(_OverviewDB.begin(), _OverviewDB.end());
		_first = f;
		_last = l;
		_RemoteServer->overviewdb(this, f, l);
	} else {
		if (_first < f) {
			_OverviewDB.erase(_OverviewDB.begin(),
					  _OverviewDB.lower_bound(f));
		}
		if (_last < l) {
			artnum_t old_last = _last;
			_first = f;
			_last = l;
			_RemoteServer->overviewdb(this, old_last + 1, l);
		}
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
