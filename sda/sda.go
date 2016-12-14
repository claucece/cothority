/*
Package sda is the Secure Distributed API which offers a simple framework for generating
your own distributed systems. It is based on a description of your protocol
and offers sending and receiving messages, handling trees and host-lists, and
easy deploying to Localhost, Deterlab or a real-system.

SDA is based on the following pieces:

- Local* - offers the user-interface to the API for deploying your protocol
locally and for testing
- Node / ProtocolInstance - gives an interface to define your protocol
- Host - handles all network-connections
- network - uses secured connections between hosts

If you just want to use an existing protocol, usually the SDA-part is enough.
If you want to create your own protocol, you have to learn how to use the
ProtocolInstance.
*/
package sda

// Version of the sda.
const Version = "0.9.2"
