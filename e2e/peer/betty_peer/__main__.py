"""Run scripted Bluetooth devices for Betty's end-to-end tests.

    python -m betty_peer --transport virtual --device sensor:"Betty Sensor"
    python -m betty_peer --transport usb:0 --device speaker:"Betty Speaker"

With --transport virtual (Linux only), the peer also creates the computer's
Bluetooth controller: a software controller attached to the kernel through
/dev/vhci, on the same virtual link as the scripted devices. Any number of
devices may run. With any other Bumble transport, such as usb:0, the one
scripted device runs on that real controller and talks to the computer over
the air.

The peer prints one JSON event per line on stdout and reads one JSON command
per line on stdin:

    {"event": "ready", "host": "F0:E2:01:00:00:00", "devices": [...]}
    {"cmd": "set_battery", "device": 0, "level": 42}
    {"cmd": "notify", "device": 0, "value": "01ff"}
    {"cmd": "accept_pairing", "device": 0, "accept": false}
    {"cmd": "drop", "device": 0}
    {"cmd": "connect_host", "device": 0}
    {"cmd": "quit"}

Each command is answered with {"event": "done", "cmd": ...} or
{"event": "error", "cmd": ..., "message": ...}. Devices also report
"connected", "disconnected", "paired", "pairing_failed", "written", and
"rfcomm_opened" events.
"""

import argparse
import asyncio
import json
import sys
import threading

from bumble.device import Device
from bumble.hci import Address
from bumble.host import Host
from bumble.link import LocalLink
from bumble.transport import open_transport
from bumble.transport.common import AsyncPipeSink

from .controller import InquiryController
from .personas import KINDS, Persona


def virtual_address(seed: int, index: int) -> str:
    """An address on the virtual link: index 0 is the computer's controller.

    The seed varies addresses between runs, so the host's Bluetooth stack never
    mixes up a device with one it cached in an earlier run.
    """
    return f"F0:E2:{seed % 256:02X}:00:00:{index:02X}"


def emit(event: dict) -> None:
    sys.stdout.write(json.dumps(event) + "\n")
    sys.stdout.flush()


def parse_device(spec: str) -> tuple[str, str]:
    kind, separator, name = spec.partition(":")
    if not separator or kind not in KINDS or not name:
        raise argparse.ArgumentTypeError(f"--device must be KIND:NAME with KIND one of {', '.join(KINDS)}")
    return kind, name


async def create_virtual(specs, seed: int) -> tuple[list[Persona], str, object]:
    transport = await open_transport("vhci")
    link = LocalLink()
    host_address = virtual_address(seed, 0)
    InquiryController(
        "computer", host_source=transport.source, host_sink=transport.sink, link=link, public_address=host_address
    )
    personas = []
    for index, (kind, name) in enumerate(specs):
        address = virtual_address(seed, index + 1)
        controller = InquiryController(f"device{index}", link=link, public_address=address)
        # The same bytes serve as the Classic public address and, with the top
        # two bits set as here, as a valid LE random static address.
        device = Device(
            name=name,
            address=Address(address, Address.RANDOM_DEVICE_ADDRESS),
            host=Host(controller, AsyncPipeSink(controller)),
        )
        personas.append(KINDS[kind](index, name, device, emit))
    return personas, host_address, transport


async def create_over_the_air(spec, transport_name: str, host_address: str | None, seed: int):
    if len(spec) != 1:
        raise SystemExit("a real controller runs exactly one --device")
    kind, name = spec[0]
    transport = await open_transport(transport_name)
    # The controller supplies the Classic public address; this is the LE
    # random static address, varied by seed like the virtual addresses.
    address = Address(virtual_address(seed, 1), Address.RANDOM_DEVICE_ADDRESS)
    device = Device.with_hci(name, address, transport.source, transport.sink)
    return [KINDS[kind](0, name, device, emit)], host_address, transport


async def run_command(personas: list[Persona], host_address: str | None, command: dict) -> bool:
    name = command.get("cmd")
    if name == "quit":
        return False
    persona = personas[int(command.get("device", 0))]
    if name == "set_battery":
        await persona.set_battery(int(command["level"]))
    elif name == "notify":
        await persona.notify(bytes.fromhex(command["value"]))
    elif name == "accept_pairing":
        persona.accept_pairing = bool(command["accept"])
    elif name == "drop":
        await persona.drop()
    elif name == "connect_host":
        if not host_address:
            raise ValueError("connect_host needs --host-address with a real controller")
        await persona.connect_host(host_address)
    else:
        raise ValueError(f"unknown command {name!r}")
    return True


def read_commands(loop: asyncio.AbstractEventLoop, queue: asyncio.Queue) -> None:
    # A thread, because asyncio cannot read a pipe on stdin on every platform.
    for line in sys.stdin:
        if line.strip():
            loop.call_soon_threadsafe(queue.put_nowait, line)
    loop.call_soon_threadsafe(queue.put_nowait, None)


async def main() -> None:
    parser = argparse.ArgumentParser(prog="betty_peer", description=__doc__.split("\n\n")[0])
    parser.add_argument("--transport", required=True, help="'virtual', or a Bumble transport such as usb:0")
    parser.add_argument("--device", action="append", type=parse_device, required=True, metavar="KIND:NAME")
    parser.add_argument("--host-address", help="the computer's Bluetooth address, for connect_host over the air")
    parser.add_argument("--seed", type=int, default=0, help="varies virtual addresses between runs")
    args = parser.parse_args()

    if args.transport == "virtual":
        personas, host_address, transport = await create_virtual(args.device, args.seed)
    else:
        personas, host_address, transport = await create_over_the_air(
            args.device, args.transport, args.host_address, args.seed
        )
    for persona in personas:
        await persona.start()
    emit(
        {
            "event": "ready",
            "host": host_address,
            "devices": [{"index": p.index, "kind": p.kind, "name": p.name, "address": p.address} for p in personas],
        }
    )

    queue: asyncio.Queue = asyncio.Queue()
    threading.Thread(target=read_commands, args=(asyncio.get_running_loop(), queue), daemon=True).start()
    while (line := await queue.get()) is not None:
        try:
            command = json.loads(line)
            keep_running = await run_command(personas, host_address, command)
        except Exception as error:  # report every failure to the test, then go on
            emit({"event": "error", "cmd": line.strip(), "message": str(error)})
            continue
        emit({"event": "done", "cmd": command.get("cmd")})
        if not keep_running:
            break
    transport.close()


if __name__ == "__main__":
    asyncio.run(main())
