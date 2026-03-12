Create an agent team for this feature:

Teammate "ai-builder" should should implement the issue 2.8 from @docs/phase-2-user-surfaces.md. Use the ai-dev skill. They should be consulted about any AI decisions.

Teammate "apple-builder" should use the apple-dev skill. They should be consulted about any iOS/macOS and other Apple OS decisions.

Teammate "ai-builder" should use the ai-dev skill. They should be consulted about any AI decisions.

Teammate "reviewer" should wait for both "apple-builder" and "ai-builder" to finish, then review 
all changed files. Use both the apple-dev and ai-dev skills. Read-only — 
don't modify any files.

After the review Teammate "apple-builder" should fix all the comments and this process needs to be repeated till "reviewer" is satisfied and gives the APPROVED status.
