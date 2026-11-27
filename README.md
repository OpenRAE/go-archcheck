# go-archcheck

To get started and run a scenario, see [LilRÆ](https://github.com/openrae/lilrae).

`archcheck` checks a Go module against its architecture model, `architecture/model.yaml`.
The model states the layers, the components, the external modules that each layer can use, and the forbidden imports and symbols.
Each structural change to the model needs a design record in the same change.

## Install and run

```sh
go install github.com/OpenRAE/go-archcheck/cmd/archcheck@VERSION
archcheck
```

Run the command from the module root.
The exit status is 0 with no findings, 1 with findings or an error, and 2 for a usage error.

To reserve the next design number for an issue, so that two branches cannot use the same number:

```sh
GH_TOKEN=... archcheck reserve -repo owner/name -issue N
```

## Documentation

- [Model format, checks and flags](docs/model.md)
- [Design decisions](docs/decisions/README.md)
- [The model of this repository](architecture/model.yaml)

## Contribute

See [`CONTRIBUTING.md`](CONTRIBUTING.md), [`SECURITY.md`](SECURITY.md) and the [code of conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE)
