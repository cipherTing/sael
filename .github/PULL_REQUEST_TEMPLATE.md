<!--
Thanks for the pull request. A few things make review much faster.
-->

## What and why

<!-- What changes, and what problem it solves. Link an issue if there is one. -->

## How it was tested

<!--
"make check" passing is the baseline. Say what else you did: a new test, a live
call against a real endpoint, a manual check of the wire bytes.
-->

## Checklist

- [ ] `make check` passes
- [ ] New behaviour has a test that fails without the change
- [ ] No new dependency, or a reason in the description that survives being
      written down
- [ ] Nothing about a specific host is baked in — `BaseURL` stays configuration
- [ ] Any new comment explaining a decision against a live API says which host
      it was observed on, and whether it was verified or is transcribed

## If this changes the wire contract

<!--
The wire contract lives in sdk/doc.go and is the source of truth for every
file in the package. If this pull request changes it, say what was observed and
how, and update doc.go in the same commit.
-->
