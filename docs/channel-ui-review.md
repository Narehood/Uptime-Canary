# Notification channel UI evidence

Channel management puts destinations and primary actions first. Setup follows provider, destination, then alert behavior. Tests identify the destination before sending, and interrupted submissions lead to inspection while preserving unsaved settings.

These screenshots show actual React/CSS with a private in-memory API and simulated data. Desktop viewports are 1440 x 1000 and mobile viewports 390 x 844; the screenshot tool scales some PNGs. Credentials are masked. No Go process, application database, or external notification was used for these captures.

| Flow | Before | Current |
| --- | --- | --- |
| Create a channel, desktop | ![Original provider catalogue](assets/channel-ui-before-create-desktop.png) | ![Compact provider-first setup](assets/channel-ui-after-create-desktop.png) |
| Manage channels, mobile | ![Original delivery explanations before channels](assets/channel-ui-before-list-mobile.png) | ![Channels and actions near the top](assets/channel-ui-after-list-mobile.png) |
| Home Assistant setup, mobile | ![Original fragmented automation warning](assets/channel-ui-before-warning-mobile.png) | ![Readable automation warning and disclosed reference details](assets/channel-ui-after-warning-mobile.png) |

The before captures use commit `cc0e7d5bcda2e070cdc39259b00db3c37e3e8531`. Current captures use product commit `155b34676eb4612dc71429cf8112ec375d3c36e8`, with source fingerprint `be4f3e2b8c76ed581c130de44c6bb66049401f83ba185c13dddb28d5fd729947`. The complete local set contains 72 freshly captured states covering both themes, validation, mixed recipient outcomes, failed reads, uncertain mutations, and draft retention. Its manifest records every PNG hash, byte count and dimensions, plus hashes for eight product files. The three current images above are copies of the reviewed captures.

Three independent GPT-6.1 Sol reviewers on xhigh each scored this current notification UI **9/10** and approved the source in round 23. They inspected all 72 current and eight original images, verified provenance, and were blinded to other scores, prior ratings and the requested threshold. Opus reviews were waived by the user. Full review history is recorded in [PR #34](https://github.com/Narehood/Uptime-Canary/pull/34).

Observed local checks: workspace types, 47 focused automation contract groups, and 28 web-image pure suites pass at the captured source fingerprint. DOM measurements confirm 44px mobile segmented controls, delivery filters and primary notification actions. Recorded interactions cover precise error targeting, retained outcomes, guarded history/context changes, save readback, and rejected-sign-in Leave/Keep/Discard. Pristine forms and successful latest-settings recovery have no error summary; genuine failures retain focused summaries. All captured pages fit their viewport horizontally.

Hosted Linux Go/Chrome, native Go and container execution evidence is recorded against the relevant commits in [PR #34](https://github.com/Narehood/Uptime-Canary/pull/34). Results from an earlier commit do not validate a later head. Frontend captures and component checks do not establish real provider delivery, complete pointer or screen-reader coverage, OS unload-dialog behavior, control-plane rejected-authentication runtime behavior, or complete Go/SQLite HA release validation.
