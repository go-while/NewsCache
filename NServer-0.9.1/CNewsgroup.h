#ifndef __CNewsgroup_h__
#define __CNewsgroup_h__

#include "NServer.h"
#include "NVNewsgroup.h"

/**
 * \class CNewsgroup
 * \author Thomas Gschwind
 *
 * \bug method documentation are mising.
 */
class CNewsgroup:public NVNewsgroup {
  protected:
	nvtime_t _TTLGroup;
	RServer *_RServer;

	void sUpdateGroupInfo(artnum_t *infof, artnum_t *infol);
	void sUpdateOverview(void);

	void updateOverview(void) {
		VERB(slog.
		     p(Logger::Debug) << "CNewsgroup::updateOverview()\n");
		nvtime_t mt;
		 getmtime(&mt);
		if (mt + _TTLGroup >= nvtime(NULL))
			 return;
		 NVArray::lock(NVcontainer::ExclLock);
		 sUpdateOverview();
		 NVArray::lock(NVcontainer::UnLock);
	}
	void listgroup(char *lstgrp, artnum_t f, artnum_t l);
	virtual Article *retrievearticle(artnum_t nbr);

  public:
	CNewsgroup(RServer * nsrvr, OverviewFmt * fmt,
		   const char *spooldir, const char *name):NVNewsgroup(fmt,
								       spooldir,
								       name)
	{
		_RServer = nsrvr;
	}

	virtual void setttl(nvtime_t grp) {
		_TTLGroup = grp;
	}

	virtual void setsize(artnum_t f, artnum_t l) {
		lock(NVcontainer::ShrdLock);
		if (f != arrfst || l != arrlst) {
			NVNewsgroup::setsize(f, l);
			setmtime(0, 1);
		}
		lock(NVcontainer::UnLock);
	}

	virtual void prefetchGroup(int lockgrp = 1);

	virtual void prefetchOverview(void) {
		VERB(slog.
		     p(Logger::
		       Debug) << "CNewsgroup::prefetchOverview()\n");
		NVArray::lock(NVcontainer::ExclLock);
		sUpdateOverview();
		NVArray::lock(NVcontainer::UnLock);
	}

	virtual const char *getover(artnum_t nbr) {
		updateOverview();
		return NVNewsgroup::getover(nbr);
	}

	virtual void printheaderdb(std::ostream & os,
				   const char *header,
				   artnum_t f = 0, artnum_t l =
				   ARTNUM_MAX) {
		updateOverview();
		NVNewsgroup::printheaderdb(os, header, f, l);
	}

	virtual void printlistgroup(std::ostream & os) {
		updateOverview();
		NVNewsgroup::printlistgroup(os);
	}

	virtual void printover(std::ostream & os, artnum_t nbr) {
		updateOverview();
		NVNewsgroup::printover(os, nbr);
	}

	virtual void printoverdb(std::ostream & os, artnum_t f =
				 0, artnum_t l = ARTNUM_MAX) {
		updateOverview();
		NVNewsgroup::printoverdb(os, f, l);
	}
};

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
