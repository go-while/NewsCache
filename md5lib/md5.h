#ifndef MD5_H
#define MD5_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif
	/* Exactly 32 bits. This was unsigned long, which is 64 bits on
	 * 64-bit systems and broke the MD5 code there. */
	typedef uint32_t uint32;

	struct MD5Context {
		uint32 buf[4];
		uint32 bits[2];
		unsigned char in[64];
	};

	void MD5Init(struct MD5Context *context);
	void MD5Update(struct MD5Context *context,
		       unsigned char const *buf, unsigned len);
	void MD5Final(unsigned char digest[16],
		      struct MD5Context *context);
	void MD5Transform(uint32 buf[4], uint32 const in[16]);

	typedef struct MD5Context MD5_CTX;
#ifdef __cplusplus
}
#endif
#endif				/* !MD5_H */

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
