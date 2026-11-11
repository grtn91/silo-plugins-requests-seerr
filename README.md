# Silo Requests: Seerr

A Silo `request_router.v1` plugin that fulfills content requests by submitting
them to a [Seerr](https://github.com/seerr-team/seerr) (Overseerr/Jellyseerr-
compatible) instance. Seerr manages its own Sonarr/Radarr; this plugin is a thin
adapter.

## Connection config

Each connection carries a Seerr **base URL** + **API key** (host chrome) and
these plugin settings:

- **Attribute requests to** (`requester_mode`): the API-key owner, or the Seerr
  user matching the Silo requester's email (created if missing).
- **Movies** and **Series**: the Radarr or Sonarr server, quality profile and
  root folder requests get. See
  [Choosing the server, quality profile and root folder](#choosing-the-server-quality-profile-and-root-folder).
- **Storage groups** (`storage_groups_enabled`): let the plugin choose the
  root folder by free space, from named groups of folders. See
  [Storage groups](#storage-groups).

## API key requirement

The API key (Settings → General in Seerr) **must belong to a Seerr admin /
auto-approve user**. Silo is the sole approval authority; requests created via
the API auto-approve and hand off to Seerr's Sonarr/Radarr immediately. A
non-admin key would leave requests pending in Seerr (visible as `queued` in Silo
that never advances). `TestConnection` (`GET /api/v1/auth/me`) surfaces an
invalid key.

## How requests map

- Each title becomes **one** standard Seerr request (`is4k: false`), whatever
  qualities Silo asks for. A Radarr or Sonarr server holds one copy of a title
  and its quality profile decides the resolution, so one server can serve both
  HD and 4K: pick a quality profile that allows 4K.
- When Silo asks for an HD and a 4K copy, both of Silo's targets follow that
  one request and complete together.
- `series` → Seerr `mediaType: "tv"` with `seasons: "all"`; movies use
  `mediaType: "movie"`. Media is identified by **TMDB id**.
- A duplicate (HTTP 409) is treated as already-queued; the plugin recovers the
  existing Seerr request id so Silo can track it. The existing request keeps
  the server, profile and folder it was made with.

### Upgrading from 0.1

Earlier versions sent a 2160p quality as a separate `is4k: true` request to
Seerr's 4K server. That is gone, with the `supports_4k`, `radarr_4k_server_id`
and `sonarr_4k_server_id` settings; saved values are ignored.

- A Seerr with **one** Radarr/Sonarr needs nothing else: set the quality
  profile and the failed 4K requests stop.
- A Seerr with a **separate 4K server** no longer gets requests on it through
  this plugin. Servers Seerr marks as 4K cannot be picked.
- 4K requests already submitted keep being tracked to completion.

## Choosing the server, quality profile and root folder

Seerr sends a request to its default Radarr or Sonarr server, with the quality
profile and root folder set for that server in Seerr, unless the request names
others. The connection's **Movies** and **Series** sections name them:

| Field | Applies to | Sent to Seerr as |
| --- | --- | --- |
| `radarr_server_id` | movies | `serverId` |
| `radarr_profile_id` | movies | `profileId` |
| `radarr_root_folder` | movies | `rootFolder` |
| `sonarr_server_id` | series | `serverId` |
| `sonarr_profile_id` | series | `profileId` |
| `sonarr_root_folder` | series | `rootFolder` |

- Each select lists what Seerr has. **Seerr default** (the value `default`)
  keeps Seerr's own choice and sends nothing; its label shows what that
  currently is. On a route, it forces Seerr's own choice for that rule.
- Quality profiles and root folders belong to a server. The selects list those
  of the server picked in the same section, or of Seerr's default server. After
  picking another server, test the connection to load its profiles and folders.
- Seerr's override rules still run, but only fill what a request leaves unset:
  a profile or folder chosen here wins over a rule.
- A profile or folder chosen for Sonarr also applies to anime, in place of the
  anime profile and folder set in Seerr. Use a Silo routing rule on anime to
  send anime elsewhere.
- Silo request routes can override these fields per rule. A rule that matches
  a genre, a requester or older titles can pick another profile or folder
  without a second Seerr. Silo's rule editor shows all six fields on every
  rule: a Movies rule uses only the Radarr fields and a Series rule only the
  Sonarr ones, so leave the others on Server default. Silo asks the plugin
  once per quality tier, so a rule whose HD and 4K destinations differ gets
  the one that is asked first; give both the same settings.
- A server Seerr does not have or marks as 4K, or a profile or folder the
  server does not have, fails the request's targets with a message saying
  which field is wrong. Seerr would otherwise use its own default without
  saying so. Saving the connection checks the picks when Seerr responds; an
  unreachable Seerr does not block saving. Optional lookups share a ten-second
  budget and return earlier when the caller's deadline is shorter.
- The plugin's `TestConnection` RPC also checks the picks. Silo's current web
  **Test** button loads the choices instead, so its success message does not
  confirm that the selected values are valid or Seerr is reachable.
- Checking a profile or folder asks Seerr for the server's details, and Seerr
  asks Radarr or Sonarr. A request therefore fails, and Silo retries it, while
  that server is down. A connection left on Seerr default makes no such call.
- An existing request is reused only when its stored server id matches an
  explicit selection. A different or unknown destination fails with a message;
  changing a route does not move a request already submitted to Seerr.
- Seerr can reuse an id after a server is deleted. Check the connection and
  route selections whenever you replace a Radarr or Sonarr server in Seerr.

## Who chooses the profile and folder

By default every request gets the profile and folder set on the connection, or
a routing rule's. Next to each of those four settings is a **chosen by** select
(`radarr_profile_chosen_by`, `radarr_folder_chosen_by`,
`sonarr_profile_chosen_by`, `sonarr_folder_chosen_by`):

- **Admin (the setting above)**: nobody picks; the setting applies. This is the
  default.
- **Requester**: Silo's request dialog offers every quality profile (or root
  folder) of the Radarr or Sonarr server the request would go to. An admin can
  change the pick when approving.
- **Admin on approval**: the admin picks when approving the request.

A pick replaces the connection's setting and a routing rule's for that
request. The setting itself stays the preselected value, and it applies when
nobody picks: requests from a result card or the watchlist, requests approved
automatically, and several requests approved at once. A routing rule can
override who chooses, like any other select.

- This needs a Silo server that supports request options (its
  `GET /api/v2/requests/status` reports `request_options_supported`). An older
  server ignores these selects.
- Silo checks a pick against the lists the plugin reports, so a requester can
  only send a profile or folder Seerr lists for that server. The plugin checks
  it again when it submits.
- With **Requester**, everyone who may request sees the choices, including
  folder paths. Keep the folder on **Admin** or **Admin on approval** if paths
  should stay private.
- Silo offers the choice when it can name the one server a title goes to: a
  single Seerr connection, or the server a routing rule sends the title's HD
  copy to. Requests made from a card, and from a client that does not know
  request options, use the connection's settings.
- To let only some people choose, or to choose for them, use routing rules on
  the requesting account instead.

## Storage groups

A root folder setting sends every request to one folder. With several disks
you usually want requests to go where there is room. Turn on **Use storage
groups** and the plugin chooses the root folder of each request itself.

A storage group is a named list of root folders with a rule for choosing among
them. The form starts with one group, and **Add storage group** adds the
next, up to eight. A group without folders is unused, and an empty group at
the end is gone the next time the form opens. On a Silo server that does not
know the add button, all eight groups are shown.

| Setting | Key | Meaning |
| --- | --- | --- |
| Use storage groups | `storage_groups_enabled` | Shows the group sections and replaces both root folder settings with a storage group setting. |
| Minimum free space (GB) | `storage_min_free_gb` | A folder with less free space counts as full. Default 50. |
| Name | `storage_group_N_name` | For example Main storage, External storage, Fallback. |
| Radarr folders, Sonarr folders | `storage_group_N_radarr_folders`, `storage_group_N_sonarr_folders` | The group's root folders for movies and for series. |
| Choose a folder | `storage_group_N_mode` | **In order** fills the first folder, then the next. **Most free space** sends each request to the folder with the most room. |
| Storage group (movies / series) | `radarr_storage_group`, `sonarr_storage_group` | **Automatic** starts at group 1. A named group starts there instead. |

How a folder is chosen for a request:

1. The plugin reads the free space of the Radarr or Sonarr server's root
   folders from Seerr.
2. It starts at group 1, or at the group picked in the storage group setting,
   and takes a folder of that group with at least the minimum free space.
3. If the group has none, the next group is tried, and so on. A picked group
   never falls back to an earlier one.
4. If no group has room, the request fails with a message that says so. It is
   not sent to Seerr's default folder.

Things to know:

- The folder lists come from the server chosen under Movies and Series. Test
  the connection after changing a server or a group to reload the lists.
- A media type that no group has folders for keeps Seerr's default folder.
- A group folder the server no longer has is skipped for requests, and is
  reported when you save or test the connection.
- A Silo routing rule can set the storage group, for example to send anime to
  External storage.
- The groups can be put in another order with the arrows in each group's
  header. A picked group is stored by its name, so the pick follows the group.
  Renaming a group means picking it again, and two groups cannot share a name.
  A group without a name is stored by its position.
- While groups are on, nobody picks a folder for a single request: the
  **chosen by** select for the root folder is hidden, and neither requesters
  nor an approving admin are offered a folder or a group. The quality profile
  can still be left to them.
- Free space is read when the request is submitted. The plugin does not know a
  title's size, so set the minimum high enough for your largest downloads.

## Download progress

While a request is queued or downloading, the plugin reports how far its
downloads are: a phase, the bytes left, and the estimated completion time.
Seerr supplies them on the request's media (`downloadStatus`, or
`downloadStatus4k` for a 4K request made by an earlier version). Its Download Sync job refreshes them from
the Radarr and Sonarr queues, every minute by default.

- Turn on **Enable Scan** for each Radarr and Sonarr server configured in
  Seerr. Download Sync polls only those servers, so Silo shows no progress for
  the others.
- Seerr reads only the first 10 items of each queue
  ([seerr-team/seerr#3360](https://github.com/seerr-team/seerr/issues/3360)).
  On a busy queue a download can be missing, so progress is best effort.
- A season pack counts once, although Sonarr lists it under every episode.
- While any of a request's downloads has no known size yet, the size and bytes
  left are reported as 0, so Silo shows no percentage rather than an
  overstated one.
- Seerr keeps each download's queue status but not Radarr and Sonarr's import
  state. A finished download shows as importing even when its import needs
  attention in Radarr or Sonarr.

## Build / test

```
go build ./... && go test ./...
```

## Community maintenance

This is an approved community plugin maintained in the
[`Silo-Community`](https://github.com/Silo-Community) organization. Use
[GitHub Issues](https://github.com/Silo-Community/silo-plugins-requests-seerr/issues)
for support and bug reports. Security reports should follow
[`SECURITY.md`](SECURITY.md).

The plugin consumes the published
[`silo-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk); CI rejects
machine-local SDK replacement directives.

## License

Licensed under AGPL-3.0. See [`LICENSE`](LICENSE).
