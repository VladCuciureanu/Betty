// Manual retain/release: cgo compiles Objective-C without ARC, and every
// Betty .m file follows the same convention.
#if __has_feature(objc_arc)
#error "bridge_darwin.m uses manual retain/release"
#endif

#import <Foundation/Foundation.h>
#import <IOBluetooth/IOBluetooth.h>
#include "bridge_darwin.h"
#include "_cgo_export.h"
#include <stdlib.h>
#include <string.h>

static char *bettyCopyString(NSString *value) {
	const char *utf8 = value.UTF8String;
	return strdup(utf8 ? utf8 : "");
}

static IOBluetoothDevice *bettyDevice(const char *address) {
	if (!address) return nil;
	NSString *value = [NSString stringWithUTF8String:address];
	return value ? [IOBluetoothDevice deviceWithAddressString:value] : nil;
}

betty_device *betty_copy_paired_devices(int *count) {
	@autoreleasepool {
		*count = 0;
		NSArray<IOBluetoothDevice *> *paired = [IOBluetoothDevice pairedDevices];
		if (paired.count == 0) return NULL;
		betty_device *out = calloc(paired.count, sizeof(betty_device));
		if (!out) return NULL;
		int n = 0;
		for (IOBluetoothDevice *device in paired) {
			out[n].name = bettyCopyString(device.nameOrAddress);
			out[n].address = bettyCopyString(device.addressString);
			out[n].connected = device.isConnected ? 1 : 0;
			out[n].paired = device.isPaired ? 1 : 0;
			n++;
		}
		*count = n;
		return out;
	}
}

void betty_free_devices(betty_device *devices, int count) {
	if (!devices) return;
	for (int i = 0; i < count; i++) {
		free(devices[i].name);
		free(devices[i].address);
	}
	free(devices);
}

int betty_connect(const char *address) {
	@autoreleasepool {
		IOBluetoothDevice *device = bettyDevice(address);
		return device ? (int)[device openConnection] : kIOReturnNoDevice;
	}
}

int betty_disconnect(const char *address) {
	@autoreleasepool {
		IOBluetoothDevice *device = bettyDevice(address);
		return device ? (int)[device closeConnection] : kIOReturnNoDevice;
	}
}

int betty_is_connected(const char *address) {
	@autoreleasepool {
		return [bettyDevice(address) isConnected] ? 1 : 0;
	}
}

int betty_is_paired(const char *address) {
	@autoreleasepool {
		return [bettyDevice(address) isPaired] ? 1 : 0;
	}
}

@interface BettyPairDelegate : NSObject <IOBluetoothDevicePairDelegate>
@property(assign) BOOL finished;
@property(assign) IOReturn result;
@property(assign) uintptr_t agent;
@end

@implementation BettyPairDelegate
- (void)devicePairingPINCodeRequest:(id)sender {
	char pin[17] = {0};
	int length = bettyPairPIN(self.agent, pin, (int)sizeof(pin) - 1);
	if (length <= 0) {
		[sender replyPINCode:0 PINCode:NULL];
		return;
	}
	BluetoothPINCode code;
	memset(&code, 0, sizeof(code));
	memcpy(code.data, pin, (size_t)length);
	[sender replyPINCode:(ByteCount)length PINCode:&code];
}

- (void)devicePairingUserConfirmationRequest:(id)sender numericValue:(BluetoothNumericValue)numericValue {
	[sender replyUserConfirmation:bettyPairConfirm(self.agent, (unsigned int)numericValue) ? YES : NO];
}

- (void)devicePairingUserPasskeyNotification:(id)sender passkey:(BluetoothPasskey)passkey {
	bettyPairNotify(self.agent, (unsigned int)passkey);
}

- (void)devicePairingFinished:(id)sender error:(IOReturn)error {
	self.result = error;
	self.finished = YES;
}
@end

int betty_pair(const char *address, int timeout_ms, int *cancel, uintptr_t agent) {
	@autoreleasepool {
		IOBluetoothDevice *device = bettyDevice(address);
		if (!device) return kIOReturnNoDevice;
		IOBluetoothDevicePair *pair = [IOBluetoothDevicePair pairWithDevice:device];
		if (!pair) return kIOReturnNoMemory;
		BettyPairDelegate *delegate = [[BettyPairDelegate new] autorelease];
		delegate.agent = agent;
		pair.delegate = delegate;
		IOReturn started = [pair start];
		if (started != kIOReturnSuccess) {
			pair.delegate = nil;
			return (int)started;
		}
		NSDate *deadline = timeout_ms > 0 ? [NSDate dateWithTimeIntervalSinceNow:timeout_ms / 1000.0] : nil;
		IOReturn result = kIOReturnSuccess;
		while (!delegate.finished) {
			if (__atomic_load_n(cancel, __ATOMIC_SEQ_CST)) {
				result = kIOReturnAborted;
				break;
			}
			if (deadline && [deadline timeIntervalSinceNow] <= 0) {
				result = kIOReturnTimeout;
				break;
			}
			@autoreleasepool {
				[[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
			}
		}
		if (delegate.finished) {
			result = delegate.result;
		} else {
			[pair stop];
		}
		pair.delegate = nil;
		return (int)result;
	}
}

int betty_adapter(char **address, char **name, int *powered) {
	@autoreleasepool {
		IOBluetoothHostController *controller = [IOBluetoothHostController defaultController];
		if (!controller) {
			return kIOReturnNoDevice;
		}
		NSString *addressString = [controller addressAsString];
		NSString *nameString = [controller nameAsString];
		*address = addressString ? strdup([addressString UTF8String]) : NULL;
		*name = nameString ? strdup([nameString UTF8String]) : NULL;
		*powered = [controller powerState] == kBluetoothHCIPowerStateON;
		return 0;
	}
}
