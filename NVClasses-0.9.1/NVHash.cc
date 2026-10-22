#include"NVHash.h"
#include"Error.h"

#include<fcntl.h>
#include<sys/stat.h>
#include<sys/types.h>

using namespace std;

void NVHash::make_current()
{
	nvoff_t hdata;

	if (mem_p && is_current())
		return;

	NVListBase::make_current();
	if ((hdata = getdata()) == 0) {
		hashtab = NULL;
		hashsz = 0;
	} else {
		size_t bsz;
		unsigned long n;

		// the table's size, then its entries; all from the file
		if (!valid_block(hdata, &bsz) || bsz < sizeof(unsigned long)) {
			corrupt("bad hash table offset");
			return;	// (only while opening: a new file)
		}
		n = *(unsigned long *) (mem_p + hdata);
		if (n == 0 ||
		    n > (bsz - sizeof(unsigned long)) / sizeof(nvoff_t) ||
		    (want_hashsz && n != want_hashsz)) {
			corrupt("bad hash table size");
			return;
		}
		hashtab =
		    (nvoff_t *) (mem_p + hdata + sizeof(unsigned long));
		hashsz = n;
	}
}

void NVHash::sclear(void)
{
	unsigned long i;
	for (i = 0; i < hashsz; i++)
		NVListBase::sclear((char *) &(hashtab[i]) - mem_p);
}

int NVHash::sis_empty(void)
{
	unsigned long i;
	for (i = 0; i < hashsz; i++)
		if (hashtab[i])
			return 0;
	return 1;
}

void NVHash::sadd(unsigned long h, const char *data, size_t szdata)
{
	NVListBase::sprepend((char *) &(hashtab[h]) - mem_p, data, szdata);
}

void NVHash::sprint(ostream & os)
{
	unsigned long i;
	for (i = 0; i < hashsz; i++)
		NVListBase::sprint((char *) &(hashtab[i]) - mem_p, os);
}

NVHash::NVHash(const char *dbname, unsigned long hashsz, int flags)
	: NVListBase(), hashtab(NULL), hashsz(0), want_hashsz(0)
{
	open(dbname, hashsz, flags);
}

void NVHash::create_table(unsigned long n)
{
	lock(ExclLock);
	if (!hashtab) {
		nvoff_t ht = nvalloc(sizeof(unsigned long) + n * sizeof(nvoff_t));
		unsigned long i;

		setdata(ht);
		hashsz = (*(unsigned long *) (mem_p + ht)) = n;
		hashtab = (nvoff_t *) (mem_p + ht + sizeof(unsigned long));
		for (i = 0; i < hashsz; i++)
			hashtab[i] = 0;
	}
	lock(UnLock);
}

// corrupt() started a new file: it needs its table
void NVHash::reinit(void)
{
	hashtab = NULL;
	hashsz = 0;
	if (want_hashsz)
		create_table(want_hashsz);
}

void NVHash::open(const char *dbname, unsigned long nhashsz, int flags)
{
	want_hashsz = nhashsz;
	NVListBase::open(dbname, flags);
	if (!hashtab)
		create_table(nhashsz);
	// The hashsz and hashtab variables will be updated at the next
	// make_current method-invocation
}

void NVHash::clear(void)
{
	lock(ExclLock);
	sclear();
	lock(UnLock);
}

int NVHash::is_empty(void)
{
	int r;
	lock(ShrdLock);
	r = sis_empty();
	lock(UnLock);
	return r;
}

void NVHash::add(unsigned long h, const char *data, size_t szdata)
{
	lock(ExclLock);
	sadd(h, data, szdata);
	lock(UnLock);
}

void NVHash::print(ostream & os)
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
