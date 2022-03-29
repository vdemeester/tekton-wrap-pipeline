# tekton-oci-pipeline

Wrap a Pipeline to use OCI as workspace


- How to handle parallel task ?
- What differs from today ?
  If you use a workspace in several task that are not dependent on
  each other, what happens ? And what "should" happen for the oci
  wrapper ?

The way it might/should work :
- Each step adds a layer (with a diff) *and* each time it is using a
  different tag (?) or maybe it could just use 1 tag and refer to the
  previously pushed digest.
  The trick here is, we cannot know the digest before, so we have to
  rely on tag. If we use only one tag, we *may* run into trouble when
  tasks are running into parallel with the same workspace, as one will
  override the previous one. We could also "rebase" on top of the
  latest build just before pushing, which would "remove" a tiny bit
  that problem.
