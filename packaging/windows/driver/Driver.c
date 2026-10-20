#include "BettyAAP.h"

static NTSTATUS BettySubmitBrb(WDFDEVICE Device, PBRB Brb, size_t BrbSize)
{
    WDF_MEMORY_DESCRIPTOR brb;
    WDF_REQUEST_SEND_OPTIONS options;

    WDF_MEMORY_DESCRIPTOR_INIT_BUFFER(&brb, Brb, (ULONG)BrbSize);
    WDF_REQUEST_SEND_OPTIONS_INIT(&options, WDF_REQUEST_SEND_OPTION_TIMEOUT);
    WDF_REQUEST_SEND_OPTIONS_SET_TIMEOUT(&options, WDF_REL_TIMEOUT_IN_SEC(BETTY_BRB_TIMEOUT_SEC));
    return WdfIoTargetSendInternalIoctlOthersSynchronously(WdfDeviceGetIoTarget(Device), NULL,
        IOCTL_INTERNAL_BTH_SUBMIT_BRB, &brb, NULL, NULL, &options, NULL);
}

// Asks BTHENUM which remote device this PDO represents.
static NTSTATUS BettyQueryRemoteAddress(WDFDEVICE Device, BTH_ADDR *Address)
{
    BTH_DEVICE_INFO info;
    WDF_MEMORY_DESCRIPTOR output;
    NTSTATUS status;

    RtlZeroMemory(&info, sizeof(info));
    WDF_MEMORY_DESCRIPTOR_INIT_BUFFER(&output, &info, sizeof(info));
    status = WdfIoTargetSendInternalIoctlSynchronously(WdfDeviceGetIoTarget(Device), NULL,
        IOCTL_INTERNAL_BTHENUM_GET_DEVINFO, NULL, &output, NULL, NULL);
    if (!NT_SUCCESS(status)) return status;
    if (info.address == 0 || info.address > 0xffffffffffffULL) return STATUS_INVALID_ADDRESS;
    *Address = info.address;
    return STATUS_SUCCESS;
}

// Called with ChannelLock held.
static NTSTATUS BettyCloseChannelLocked(WDFDEVICE Device, PBETTY_DEVICE_CONTEXT Context)
{
    struct _BRB_L2CA_CLOSE_CHANNEL *close;
    NTSTATUS status;

    if (Context->Channel == NULL) return STATUS_SUCCESS;
    if (!Context->InterfaceReady) {
        Context->Channel = NULL;
        return STATUS_SUCCESS;
    }
    close = (struct _BRB_L2CA_CLOSE_CHANNEL *)Context->Profile.BthAllocateBrb(BRB_L2CA_CLOSE_CHANNEL, BETTY_POOL_TAG);
    if (close == NULL) return STATUS_INSUFFICIENT_RESOURCES;
    close->BtAddress = Context->RemoteAddress;
    close->ChannelHandle = Context->Channel;
    status = BettySubmitBrb(Device, (PBRB)close, sizeof(*close));
    Context->Profile.BthFreeBrb((PBRB)close);
    // The handle is unusable after a close attempt either way.
    Context->Channel = NULL;
    return status;
}

// May run at DISPATCH_LEVEL, so it only records the event; the channel is
// closed from BettyEvtDisconnectWorkItem at PASSIVE_LEVEL under ChannelLock.
static VOID BettyL2capIndication(PVOID CallbackContext, INDICATION_CODE Indication, PINDICATION_PARAMETERS Parameters)
{
    WDFDEVICE device = (WDFDEVICE)CallbackContext;
    PBETTY_DEVICE_CONTEXT context;

    if (device == NULL || Parameters == NULL || Indication != IndicationRemoteDisconnect) return;
    context = BettyGetDeviceContext(device);
    (void)InterlockedExchangePointer((PVOID volatile *)&context->PendingRemoteDisconnect, Parameters->ConnectionHandle);
    WdfWorkItemEnqueue(context->DisconnectWorkItem);
}

VOID BettyEvtDisconnectWorkItem(WDFWORKITEM WorkItem)
{
    WDFDEVICE device = (WDFDEVICE)WdfWorkItemGetParentObject(WorkItem);
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(device);
    L2CAP_CHANNEL_HANDLE disconnected;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    disconnected = InterlockedExchangePointer((PVOID volatile *)&context->PendingRemoteDisconnect, NULL);
    // Ignore a stale indication for a channel that was already closed and
    // possibly replaced by a new one.
    if (disconnected != NULL && disconnected == context->Channel) {
        (void)BettyCloseChannelLocked(device, context);
    }
    WdfWaitLockRelease(context->ChannelLock);
}

NTSTATUS BettyOpenChannel(WDFDEVICE Device, BTH_ADDR Address, USHORT Psm)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    struct _BRB_L2CA_OPEN_CHANNEL *open;
    NTSTATUS status;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    if (!context->InterfaceReady) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_DEVICE_NOT_READY;
    }
    if (Address != context->RemoteAddress) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_INVALID_ADDRESS;
    }
    if (context->Channel != NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_DEVICE_BUSY;
    }
    open = (struct _BRB_L2CA_OPEN_CHANNEL *)context->Profile.BthAllocateBrb(BRB_L2CA_OPEN_CHANNEL, BETTY_POOL_TAG);
    if (open == NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_INSUFFICIENT_RESOURCES;
    }
    open->BtAddress = context->RemoteAddress;
    open->Psm = Psm;
    open->ChannelFlags = CF_ROLE_EITHER;
    open->ConfigOut.Flags = 0;
    open->ConfigIn.Flags = 0;
    open->IncomingQueueDepth = 10;
    open->CallbackFlags = CALLBACK_DISCONNECT;
    open->Callback = BettyL2capIndication;
    open->CallbackContext = Device;
    open->ReferenceObject = WdfDeviceWdmGetDeviceObject(Device);
    status = BettySubmitBrb(Device, (PBRB)open, sizeof(*open));
    if (NT_SUCCESS(status)) {
        context->Channel = open->ChannelHandle;
    }
    context->Profile.BthFreeBrb((PBRB)open);
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

NTSTATUS BettyCloseChannel(WDFDEVICE Device)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    NTSTATUS status;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    status = BettyCloseChannelLocked(Device, context);
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

NTSTATUS BettyTransfer(WDFDEVICE Device, BOOLEAN Incoming, PVOID Buffer, ULONG *Size, ULONG TimeoutMs)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    struct _BRB_L2CA_ACL_TRANSFER *transfer;
    NTSTATUS status;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    if (context->Channel == NULL || !context->InterfaceReady) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_DEVICE_NOT_CONNECTED;
    }
    transfer = (struct _BRB_L2CA_ACL_TRANSFER *)context->Profile.BthAllocateBrb(BRB_L2CA_ACL_TRANSFER, BETTY_POOL_TAG);
    if (transfer == NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_INSUFFICIENT_RESOURCES;
    }
    transfer->BtAddress = context->RemoteAddress;
    transfer->ChannelHandle = context->Channel;
    transfer->TransferFlags = Incoming ? (ACL_TRANSFER_DIRECTION_IN | ACL_SHORT_TRANSFER_OK) : ACL_TRANSFER_DIRECTION_OUT;
    if (Incoming) {
        if (TimeoutMs == 0 || TimeoutMs > BETTY_MAX_RECEIVE_TIMEOUT_MS) TimeoutMs = BETTY_MAX_RECEIVE_TIMEOUT_MS;
        transfer->TransferFlags |= ACL_TRANSFER_TIMEOUT;
        transfer->Timeout = TimeoutMs;
    }
    transfer->BufferSize = *Size;
    transfer->Buffer = Buffer;
    transfer->BufferMDL = NULL;
    status = BettySubmitBrb(Device, (PBRB)transfer, sizeof(*transfer));
    if (NT_SUCCESS(status)) {
        *Size = transfer->BufferSize;
    }
    context->Profile.BthFreeBrb((PBRB)transfer);
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

NTSTATUS DriverEntry(PDRIVER_OBJECT DriverObject, PUNICODE_STRING RegistryPath)
{
    WDF_DRIVER_CONFIG config;
    WDF_DRIVER_CONFIG_INIT(&config, BettyEvtDeviceAdd);
    return WdfDriverCreate(DriverObject, RegistryPath, WDF_NO_OBJECT_ATTRIBUTES, &config, WDF_NO_HANDLE);
}

NTSTATUS BettyEvtDeviceAdd(WDFDRIVER Driver, PWDFDEVICE_INIT DeviceInit)
{
    WDF_OBJECT_ATTRIBUTES attributes;
    WDF_PNPPOWER_EVENT_CALLBACKS pnp;
    WDF_FILEOBJECT_CONFIG files;
    WDF_IO_QUEUE_CONFIG queue;
    WDF_WORKITEM_CONFIG workItem;
    WDFDEVICE device;
    PBETTY_DEVICE_CONTEXT context;
    NTSTATUS status;
    UNREFERENCED_PARAMETER(Driver);

    WDF_PNPPOWER_EVENT_CALLBACKS_INIT(&pnp);
    pnp.EvtDevicePrepareHardware = BettyEvtPrepareHardware;
    pnp.EvtDeviceReleaseHardware = BettyEvtReleaseHardware;
    WdfDeviceInitSetPnpPowerEventCallbacks(DeviceInit, &pnp);
    WDF_FILEOBJECT_CONFIG_INIT(&files, WDF_NO_EVENT_CALLBACK, BettyEvtFileClose, WDF_NO_EVENT_CALLBACK);
    files.AutoForwardCleanupClose = WdfFalse;
    WdfDeviceInitSetFileObjectConfig(DeviceInit, &files, WDF_NO_OBJECT_ATTRIBUTES);
    WdfDeviceInitSetDeviceType(DeviceInit, FILE_DEVICE_BLUETOOTH);
    // The stack's security descriptor comes from the INF; secure open makes
    // it apply to opens of names below the device too.
    WdfDeviceInitSetCharacteristics(DeviceInit, FILE_DEVICE_SECURE_OPEN, TRUE);
    WdfDeviceInitSetExclusive(DeviceInit, TRUE);
    WdfDeviceInitSetIoType(DeviceInit, WdfDeviceIoBuffered);
    WDF_OBJECT_ATTRIBUTES_INIT_CONTEXT_TYPE(&attributes, BETTY_DEVICE_CONTEXT);
    status = WdfDeviceCreate(&DeviceInit, &attributes, &device);
    if (!NT_SUCCESS(status)) return status;
    context = BettyGetDeviceContext(device);
    RtlZeroMemory(context, sizeof(*context));

    WDF_OBJECT_ATTRIBUTES_INIT(&attributes);
    attributes.ParentObject = device;
    status = WdfWaitLockCreate(&attributes, &context->ChannelLock);
    if (!NT_SUCCESS(status)) return status;

    WDF_WORKITEM_CONFIG_INIT(&workItem, BettyEvtDisconnectWorkItem);
    WDF_OBJECT_ATTRIBUTES_INIT(&attributes);
    attributes.ParentObject = device;
    status = WdfWorkItemCreate(&workItem, &attributes, &context->DisconnectWorkItem);
    if (!NT_SUCCESS(status)) return status;

    status = WdfDeviceCreateDeviceInterface(device, &GUID_DEVINTERFACE_BETTY_AAP, NULL);
    if (!NT_SUCCESS(status)) return status;
    WDF_IO_QUEUE_CONFIG_INIT_DEFAULT_QUEUE(&queue, WdfIoQueueDispatchSequential);
    queue.EvtIoDeviceControl = BettyEvtIoDeviceControl;
    return WdfIoQueueCreate(device, &queue, WDF_NO_OBJECT_ATTRIBUTES, WDF_NO_HANDLE);
}

NTSTATUS BettyEvtPrepareHardware(WDFDEVICE Device, WDFCMRESLIST Raw, WDFCMRESLIST Translated)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    BTH_ADDR address = 0;
    NTSTATUS status;
    UNREFERENCED_PARAMETER(Raw);
    UNREFERENCED_PARAMETER(Translated);

    status = BettyQueryRemoteAddress(Device, &address);
    if (!NT_SUCCESS(status)) return status;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    RtlZeroMemory(&context->Profile, sizeof(context->Profile));
    status = WdfFdoQueryForInterface(Device, &GUID_BTHDDI_PROFILE_DRIVER_INTERFACE,
        (PINTERFACE)&context->Profile, sizeof(context->Profile),
        BTHDDI_PROFILE_DRIVER_INTERFACE_VERSION_FOR_QI, NULL);
    context->InterfaceReady = NT_SUCCESS(status);
    context->RemoteAddress = address;
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

VOID BettyEvtFileClose(WDFFILEOBJECT FileObject)
{
    (void)BettyCloseChannel(WdfFileObjectGetDevice(FileObject));
}

NTSTATUS BettyEvtReleaseHardware(WDFDEVICE Device, WDFCMRESLIST Translated)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    UNREFERENCED_PARAMETER(Translated);

    (void)BettyCloseChannel(Device);
    // With the channel closed, a queued remote-disconnect work item finds
    // nothing to do; wait for it before the interface goes away.
    WdfWorkItemFlush(context->DisconnectWorkItem);
    WdfWaitLockAcquire(context->ChannelLock, NULL);
    if (context->InterfaceReady && context->Profile.Interface.InterfaceDereference != NULL) {
        context->Profile.Interface.InterfaceDereference(context->Profile.Interface.Context);
    }
    context->InterfaceReady = FALSE;
    WdfWaitLockRelease(context->ChannelLock);
    return STATUS_SUCCESS;
}

VOID BettyEvtIoDeviceControl(WDFQUEUE Queue, WDFREQUEST Request, size_t OutputLength, size_t InputLength, ULONG IoControlCode)
{
    WDFDEVICE device = WdfIoQueueGetDevice(Queue);
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(device);
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;
    size_t information = 0;
    PVOID input = NULL, output = NULL;
    size_t size;
    ULONG transferSize;
    UNREFERENCED_PARAMETER(InputLength);
    UNREFERENCED_PARAMETER(OutputLength);

    switch (IoControlCode) {
    case IOCTL_BETTY_GET_ADDRESS: {
        PBETTY_ADDRESS_OUTPUT out;
        status = WdfRequestRetrieveOutputBuffer(Request, sizeof(*out), (PVOID *)&out, &size);
        if (!NT_SUCCESS(status)) break;
        out->BluetoothAddress = context->RemoteAddress;
        information = sizeof(*out);
        break;
    }
    case IOCTL_BETTY_CONNECT: {
        PBETTY_CONNECT_INPUT in;
        PBETTY_CONNECT_OUTPUT out;
        status = WdfRequestRetrieveInputBuffer(Request, sizeof(*in), (PVOID *)&in, &size);
        if (!NT_SUCCESS(status)) break;
        if (in->Psm != BETTY_AAP_PSM || in->BluetoothAddress == 0 || in->BluetoothAddress > 0xffffffffffffULL) {
            status = STATUS_INVALID_PARAMETER;
            break;
        }
        if (in->BluetoothAddress != context->RemoteAddress) {
            status = STATUS_INVALID_ADDRESS;
            break;
        }
        status = WdfRequestRetrieveOutputBuffer(Request, sizeof(*out), (PVOID *)&out, &size);
        if (!NT_SUCCESS(status)) break;
        status = BettyOpenChannel(device, in->BluetoothAddress, in->Psm);
        out->Success = NT_SUCCESS(status) ? 1u : 0u;
        out->Status = status;
        information = sizeof(*out);
        status = STATUS_SUCCESS;
        break;
    }
    case IOCTL_BETTY_DISCONNECT:
        status = BettyCloseChannel(device);
        break;
    case IOCTL_BETTY_SEND:
        status = WdfRequestRetrieveInputBuffer(Request, 1, &input, &size);
        if (!NT_SUCCESS(status)) break;
        if (size > BETTY_MAX_TRANSFER) { status = STATUS_INVALID_BUFFER_SIZE; break; }
        transferSize = (ULONG)size;
        status = BettyTransfer(device, FALSE, input, &transferSize, 0);
        break;
    case IOCTL_BETTY_RECEIVE: {
        ULONG timeout = 0;
        if (NT_SUCCESS(WdfRequestRetrieveInputBuffer(Request, sizeof(BETTY_RECEIVE_INPUT), &input, &size))) {
            timeout = ((PBETTY_RECEIVE_INPUT)input)->TimeoutMs;
        }
        status = WdfRequestRetrieveOutputBuffer(Request, 1, &output, &size);
        if (!NT_SUCCESS(status)) break;
        if (size > BETTY_MAX_TRANSFER) { status = STATUS_INVALID_BUFFER_SIZE; break; }
        transferSize = (ULONG)size;
        status = BettyTransfer(device, TRUE, output, &transferSize, timeout);
        if (NT_SUCCESS(status)) information = transferSize;
        break;
    }
    }
    WdfRequestCompleteWithInformation(Request, status, information);
}
