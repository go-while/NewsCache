#include <vector>
#include "NVArray.h"
#include "Error.h"

#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <stdio.h>
#include <string.h>

using namespace std;

void NVArray::make_current()
{
	nvoff_t adata;

	if (!mem_p || !is_current())
		NVcontainer::make_current();
	if ((adata = getdata()) == 0) {
		arrtab = NULL;
		arrfst = 1;
		arrlst = 0;
	} else {
		size_t bsz;
		unsigned long f, l;

		// The index block: first and last number, then one offset
		// per number. All of it comes from the file.
		if (!valid_block(adata, &bsz) ||
		    bsz < 2 * sizeof(unsigned long)) {
			corrupt("bad index offset");
			return;	// (only while opening: a new file)
		}
		f = *(unsigned long *) (mem_p + adata);
		l = *((unsigned long *) (mem_p + adata) + 1);
		if (f <= l && l - f >= bsz / sizeof(nvoff_t) - 2) {
			corrupt("index larger than its block");
			return;
		}
		arrtab =
		    (nvoff_t *) (mem_p + adata +
				 2 * sizeof(unsigned long));
		arrfst = f;
		arrlst = l;
	}
}

void NVArray::sclear(void)
{
	unsigned long i, n;

	if (arrfst > arrlst)
		return;
	n = arrlst - arrfst + 1;
	for (i = 0; i < n; i++)
		if (arrtab[i]) {
			nvfree(arrtab[i]);
			// (left set, a later ssetsize copied the freed blocks)
			arrtab[i] = 0;
		}
}

/*
 * The bytes after the table in the table's memory block: the extra
 * bytes of ssetsize, plus whatever the allocator rounded up. NULL if
 * there is no table.
 */
char *NVArray::sextra(size_t *sz)
{
	nvoff_t adata = getdata();
	size_t blk, used;

	*sz = 0;
	if (adata == 0 || arrfst > arrlst)
		return NULL;
	// nvalloc stores the block size (header included) just before it
	blk = *(unsigned long *) (mem_p + adata - sizeof(unsigned long)) -
	    sizeof(unsigned long);
	used = (arrlst - arrfst + 1 + 2) * sizeof(unsigned long);
	if (blk <= used)
		return NULL;
	*sz = blk - used;
	return mem_p + adata + used;
}

void NVArray::sset(unsigned long i, const char *data, size_t szdata)
{
	// the new block first: when nvalloc throws (disk full), the old
	// record stays in place instead of a freed block in the index
	nvoff_t x, old = arrtab[i];

	x = nvalloc(szdata + sizeof(unsigned long));
	if (old)
		nvfree(old);
	arrtab[i] = x;
	*((unsigned long *) (mem_p + x)) = szdata;
	memcpy(mem_p + x + sizeof(unsigned long), data, szdata);
}

void NVArray::sdel(unsigned long i)
{
	if (arrtab[i]) {
		nvfree(arrtab[i]);
		arrtab[i] = 0;
	}
}

int NVArray::sis_empty(void)
{
	unsigned long i, n;

	if (arrfst > arrlst)
		return 1;
	n = arrlst - arrfst + 1;
	for (i = 0; i < n; i++)
		if (arrtab[i])
			return 0;
	return 1;
}

int NVArray::shas_element(unsigned long i)
{
	if (i < arrfst || i > arrlst)
		return 0;
	return arrtab[i - arrfst];
}

void NVArray::sget(unsigned long i, char **data, size_t * szdata)
{
	if (arrfst <= i && i <= arrlst && arrtab[i = i - arrfst]) {
		nvoff_t o = arrtab[i];
		size_t bsz;

		// a record: its size, then its data
		if (!valid_block(o, &bsz) || bsz < sizeof(unsigned long) ||
		    *(unsigned long *) (mem_p + o) > bsz - sizeof(unsigned long))
			corrupt("bad index entry");
		*szdata = *(unsigned long *) (mem_p + o);
		*data = mem_p + o + sizeof(unsigned long);
		if (!valid_record(*data, *szdata))
			corrupt("bad record");
	} else {
		*szdata = 0;
		*data = NULL;
	}
}

void NVArray::sprint(ostream & os)
{
	unsigned long i, n;

	if (arrfst > arrlst)
		return;
	n = arrlst - arrfst + 1;
	for (i = 0; i < n; i++) {
		if (arrtab[i]) {
			os << arrfst +
			    i << "/" << *(unsigned long *) (mem_p +
							    arrtab[i])
			    << ":" << mem_p + arrtab[i] +
			    sizeof(unsigned long) << endl;
		} else {
			os << arrfst + i << "/(null):" << endl;
		}
	}
}

NVArray::NVArray(const char *dbname, int flags)
	: NVcontainer()
{
	open(dbname, flags);
}

bool NVArray::overlaps_index(nvoff_t p, size_t sz)
{
	nvoff_t t = getdata();
	size_t tsz;

	if (!arrtab || !t || p == t || !valid_block(t, &tsz))
		return false;
	return p < t + tsz && t < p + sz;
}

// A corrupt free list could hand out, or take back, the index's own
// memory. (This killed the process with SIGABRT.)
nvoff_t NVArray::nvalloc(size_t rsz)
{
	nvoff_t r = NVcontainer::nvalloc(rsz);
	if (overlaps_index(r, rsz))
		corrupt("allocated a block inside the index");
	return r;
}

void NVArray::nvfree(nvoff_t p)
{
	size_t sz;

	if (valid_block(p, &sz) && overlaps_index(p, sz))
		corrupt("freeing a block inside the index");
	NVcontainer::nvfree(p);
}

void NVArray::open(const char *dbname, int flags)
{
	NVcontainer::open(dbname, flags);
}

void NVArray::ssetsize(unsigned long fst, unsigned long lst, size_t extra)
{
	nvoff_t *ndata;
	nvoff_t p, q;
	unsigned long i, n;

	p = nvalloc((lst - fst + 1 + 2) * sizeof(unsigned long) + extra);
	if (extra)
		memset(mem_p + p + (lst - fst + 1 + 2) * sizeof(unsigned long),
		       0, extra);
	q = getdata();
	ndata = (nvoff_t *) (mem_p + p);
	ndata[0] = fst;
	ndata[1] = lst;
	n = lst - fst + 1;
	if (arrfst <= arrlst) {
		for (i = 0; i < n; i++) {
			if (arrfst <= i + fst && i + fst <= arrlst) {
				ndata[2 + i] = arrtab[i + fst - arrfst];
			} else {
				ndata[2 + i] = 0;
			}
		}
	} else {
		for (i = 0; i < n; i++)
			ndata[2 + i] = 0;
	}
	arrfst = fst;
	arrlst = lst;
	setdata(p);
	arrtab = ndata + 2;
	if (q)
		nvfree(q);
}

int NVArray::setsize(unsigned long fst, unsigned long lst, int flags)
{
	// Usually, the user should have already set an excludive lock
	// Otherwise, it cannot be guaranteed that no other process
	// changes the arraysize again
	// This is just to ensure that the NVArray cannot get inconsistent
	// even in the case where the user uses this database incorrectly.
	NVLock guard(*this, ExclLock);
	std::vector < nvoff_t > drop;	// records outside [fst,lst]
	unsigned long i, n;

	if (arrfst <= arrlst) {
		n = arrlst - arrfst + 1;
		for (i = 0; i < n; i++) {
			if (arrtab[i] &&
			    (i + arrfst < fst || i + arrfst > lst)) {
				if (!(flags & force))
					return -1;
				drop.push_back(arrtab[i]);
			}
		}
	}
	// the new index first (it copies only the entries within the new
	// range), then the records that fell out: when nvalloc throws, the
	// old index is still whole. (The loop for fst > arrfst freed one
	// entry too many, the record of fst itself, and kept it.)
	ssetsize(fst, lst);
	for (i = 0; i < drop.size(); i++)
		nvfree(drop[i]);
	return 0;
}

void NVArray::clear(void)
{
	NVLock guard(*this, ExclLock);
	sclear();
}

void NVArray::set(unsigned long i, const char *data, size_t szdata)
{
	NVLock guard(*this, ExclLock);
	sset(i, data, szdata);
}

void NVArray::del(unsigned long i)
{
	lock(ExclLock);
	sdel(i);
	lock(UnLock);
}

int NVArray::is_empty(void)
{
	int r;
	lock(ShrdLock);
	r = sis_empty();
	lock(UnLock);
	return r;
}

int NVArray::has_element(unsigned long i)
{
	int r;
	lock(ShrdLock);
	r = shas_element(i);
	lock(UnLock);
	return r;
}

void NVArray::get(unsigned long i, const char **data, size_t * szdata)
{
	lock(ShrdLock);
	sget(i, (char **) data, szdata);
	lock(UnLock);
}

void NVArray::print(ostream & os)
{
	lock(ShrdLock);
	sprint(os);
	lock(UnLock);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
