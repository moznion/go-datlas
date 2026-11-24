# Changelog

## [v0.2.0](https://github.com/moznion/go-datlas/compare/v0.1.0...v0.2.0) - 2026-11-24

- ci: attach attested cross-built binaries to tagpr releases by @moznion in https://github.com/moznion/go-datlas/pull/25
- feat!: add db:"<column>,insertonly" to write a column on insert only by @moznion in https://github.com/moznion/go-datlas/pull/22
- feat: add touch=<column> to set a column to the current time on update by @moznion in https://github.com/moznion/go-datlas/pull/23
- feat: add skip_unchanged=true to leave rows already holding the values by @moznion in https://github.com/moznion/go-datlas/pull/24
- fix: fail to apply records whose logical key is NULL by @moznion in https://github.com/moznion/go-datlas/pull/28
- feat: add DiffMasterData and DiffSeedData to preview what applying would change by @moznion in https://github.com/moznion/go-datlas/pull/27

## [v0.1.0](https://github.com/moznion/go-datlas/compare/v0.0.2...v0.1.0) - 2026-11-21

- feat: generate SQL applying master and seed data to a database by @moznion in https://github.com/moznion/go-datlas/pull/18
- ci: Bump actions/setup-go from 7.0.0 to 7.1.0 in the github-actions group by @dependabot[bot] in https://github.com/moznion/go-datlas/pull/20

## [v0.0.2](https://github.com/moznion/go-datlas/compare/v0.0.1...v0.0.2) - 2026-11-19

- feat: support datlas:"nonzero" on encoding.TextUnmarshaler types by @moznion in https://github.com/moznion/go-datlas/pull/11
- feat: add datlas:"elem=nonzero" for elements of slices, arrays and maps by @moznion in https://github.com/moznion/go-datlas/pull/12
- feat: support uniqueness constraints besides the logical key by @moznion in https://github.com/moznion/go-datlas/pull/13
- feat: support encoding.TextUnmarshaler types as logical keys by @moznion in https://github.com/moznion/go-datlas/pull/14
- feat: support encoding.TextUnmarshaler types in datlas:"unique" by @moznion in https://github.com/moznion/go-datlas/pull/16
- docs: complete the README and example for v0.0.2 features by @moznion in https://github.com/moznion/go-datlas/pull/17

## [v0.0.1](https://github.com/moznion/go-datlas/commits/v0.0.1) - 2026-11-18

- ci: pass fixture.yaml config to datlas fixture check by @moznion in https://github.com/moznion/go-datlas/pull/1
- declarative: generate master key constants and dataset dependency order by @moznion in https://github.com/moznion/go-datlas/pull/2
- fixture: add the unique flag to fixture tags by @moznion in https://github.com/moznion/go-datlas/pull/3
- fixture: add custom generators with gen=FUNC by @moznion in https://github.com/moznion/go-datlas/pull/4
- fixture: link foreign keys with from= and ref= by @moznion in https://github.com/moznion/go-datlas/pull/5
- deps: update Go modules and GitHub Actions by @moznion in https://github.com/moznion/go-datlas/pull/6
- declarative: follow symbolic links to environments and data files by @moznion in https://github.com/moznion/go-datlas/pull/7
- ci: add Dependabot for GitHub Actions and Go modules by @moznion in https://github.com/moznion/go-datlas/pull/8
- ci: introduce Songmu/tagpr for release management by @moznion in https://github.com/moznion/go-datlas/pull/9
