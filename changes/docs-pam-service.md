Documentation:
* Explain that `PAMServicename` must name an installed service, a missing service may fall back to the system's `other` policy depending on PAM configuration, values are not quoted, and the `pam_unix` log hint may not apply to failures from other modules.
