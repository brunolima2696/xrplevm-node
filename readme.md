# XRPL EVM Sidechain Node — Fork

Este repositório é um fork do [`xrplevm/node`](https://github.com/xrplevm/node), adaptado para os testes de interoperabilidade do projeto.

## Light client Besu/QBFT

O fork incorpora um light client IBC para redes Besu com consenso QBFT, compatível com IBC-Go v10. A implementação permite que a XRPL EVM Sidechain:

- registre o cliente `hb-qbft`;
- valide cabeçalhos e assinaturas do conjunto de validadores QBFT;
- mantenha os estados de cliente e consenso necessários pelo IBC.

O código está em [`lightclients/besu-qbft`](lightclients/besu-qbft).

## Light client Fabric/MSP

O fork também incorpora um light client IBC para redes Hyperledger Fabric. A implementação permite que a XRPL EVM Sidechain:

- registre o cliente `fabric-msp`;
- valide identidades, certificados e políticas de endosso MSP;
- verifique compromissos IBC presentes nos read-write sets endossados pelo chaincode.

O código está em [`lightclients/fabric-msp`](lightclients/fabric-msp).
