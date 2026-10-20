#pragma once

#include <ntddk.h>
#include <wdf.h>
// initguid.h must come before the Bluetooth headers so this file defines
// GUID_BTHDDI_PROFILE_DRIVER_INTERFACE instead of only declaring it.
#include <initguid.h>
#include <bthdef.h>
#include <bthguid.h>
#include <bthddi.h>
#include <bthioctl.h>

// {B377A1A0-8CB0-4A5C-9BA2-53BDCC4C5E21}
DEFINE_GUID(GUID_DEVINTERFACE_BETTY_AAP,
    0xb377a1a0, 0x8cb0, 0x4a5c, 0x9b, 0xa2, 0x53, 0xbd, 0xcc, 0x4c, 0x5e, 0x21);

#define FILE_DEVICE_BETTY 0x8000
#define BETTY_POOL_TAG 'ytTB'
#define BETTY_AAP_PSM 0x1001
#define BETTY_MAX_TRANSFER 65535
// BRB requests are cancelled after this long. Receive timeouts are capped
// below it so the Bluetooth stack's own timeout fires first.
#define BETTY_BRB_TIMEOUT_SEC 10
#define BETTY_MAX_RECEIVE_TIMEOUT_MS 8000

// The I/O manager checks these access bits against the handle's granted
// access, which the device's security descriptor (see BettyAAP.inf) limits
// to SYSTEM, administrators and interactive users. internal/l2cap mirrors
// these values.
#define IOCTL_BETTY_CONNECT     CTL_CODE(FILE_DEVICE_BETTY, 0x800, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_BETTY_DISCONNECT  CTL_CODE(FILE_DEVICE_BETTY, 0x801, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_BETTY_SEND        CTL_CODE(FILE_DEVICE_BETTY, 0x802, METHOD_BUFFERED, FILE_WRITE_DATA)
#define IOCTL_BETTY_RECEIVE     CTL_CODE(FILE_DEVICE_BETTY, 0x803, METHOD_BUFFERED, FILE_READ_DATA)
#define IOCTL_BETTY_GET_ADDRESS CTL_CODE(FILE_DEVICE_BETTY, 0x804, METHOD_BUFFERED, FILE_READ_DATA)

#include <pshpack1.h>
typedef struct _BETTY_CONNECT_INPUT {
    ULONGLONG BluetoothAddress; // must equal the address the device is bound to
    USHORT Psm;
} BETTY_CONNECT_INPUT, *PBETTY_CONNECT_INPUT;

typedef struct _BETTY_CONNECT_OUTPUT {
    ULONG Success;
    LONG Status;
} BETTY_CONNECT_OUTPUT, *PBETTY_CONNECT_OUTPUT;

typedef struct _BETTY_RECEIVE_INPUT {
    ULONG TimeoutMs;
} BETTY_RECEIVE_INPUT, *PBETTY_RECEIVE_INPUT;

typedef struct _BETTY_ADDRESS_OUTPUT {
    ULONGLONG BluetoothAddress;
} BETTY_ADDRESS_OUTPUT, *PBETTY_ADDRESS_OUTPUT;
#include <poppack.h>

typedef struct _BETTY_DEVICE_CONTEXT {
    BTH_PROFILE_DRIVER_INTERFACE Profile;
    // Remote device this instance was enumerated for by BTHENUM. Fixed for
    // the lifetime of the device; CONNECT accepts no other address.
    BTH_ADDR RemoteAddress;
    BOOLEAN InterfaceReady;
    // ChannelLock guards Channel, InterfaceReady and Profile use.
    WDFWAITLOCK ChannelLock;
    L2CAP_CHANNEL_HANDLE Channel;
    // Set at any IRQL by the L2CAP callback when the remote side closes the
    // channel; consumed by DisconnectWorkItem at PASSIVE_LEVEL.
    L2CAP_CHANNEL_HANDLE volatile PendingRemoteDisconnect;
    WDFWORKITEM DisconnectWorkItem;
} BETTY_DEVICE_CONTEXT, *PBETTY_DEVICE_CONTEXT;
WDF_DECLARE_CONTEXT_TYPE_WITH_NAME(BETTY_DEVICE_CONTEXT, BettyGetDeviceContext)

DRIVER_INITIALIZE DriverEntry;
EVT_WDF_DRIVER_DEVICE_ADD BettyEvtDeviceAdd;
EVT_WDF_DEVICE_PREPARE_HARDWARE BettyEvtPrepareHardware;
EVT_WDF_DEVICE_RELEASE_HARDWARE BettyEvtReleaseHardware;
EVT_WDF_IO_QUEUE_IO_DEVICE_CONTROL BettyEvtIoDeviceControl;
EVT_WDF_FILE_CLOSE BettyEvtFileClose;
EVT_WDF_WORKITEM BettyEvtDisconnectWorkItem;

NTSTATUS BettyOpenChannel(WDFDEVICE Device, BTH_ADDR Address, USHORT Psm);
NTSTATUS BettyCloseChannel(WDFDEVICE Device);
NTSTATUS BettyTransfer(WDFDEVICE Device, BOOLEAN Incoming, PVOID Buffer, ULONG *Size, ULONG TimeoutMs);
