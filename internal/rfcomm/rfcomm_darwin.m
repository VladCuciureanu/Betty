// Manual retain/release: cgo compiles Objective-C without ARC, and every
// Betty .m file follows the same convention.
#if __has_feature(objc_arc)
#error "this file uses manual retain/release"
#endif

#import <Foundation/Foundation.h>
#import <IOBluetooth/IOBluetooth.h>
#include "rfcomm_darwin.h"

@interface BettyRFCOMMRecord : NSObject <IOBluetoothRFCOMMChannelDelegate>
@property(retain) IOBluetoothRFCOMMChannel *channel;
@property(retain) NSCondition *condition;
@property(retain) NSMutableData *buffer;
@property BOOL closed;
@end

@implementation BettyRFCOMMRecord
- (id)init { if ((self=[super init])) { self.condition=[[NSCondition new] autorelease]; self.buffer=[NSMutableData data]; } return self; }
- (void)dealloc { [_channel release]; [_condition release]; [_buffer release]; [super dealloc]; }
- (void)rfcommChannelData:(IOBluetoothRFCOMMChannel *)channel data:(void *)data length:(size_t)length { [self.condition lock]; [self.buffer appendBytes:data length:length]; [self.condition signal]; [self.condition unlock]; }
- (void)rfcommChannelClosed:(IOBluetoothRFCOMMChannel *)channel { [self.condition lock]; self.closed=YES; [self.condition broadcast]; [self.condition unlock]; }
@end

static NSMutableDictionary *records;
static long long nextID=1;
static NSLock *recordsLock;
static void initializeRecords(void){static dispatch_once_t once;dispatch_once(&once,^{records=[NSMutableDictionary new];recordsLock=[NSLock new];});}
static BettyRFCOMMRecord *recordFor(long long identifier){initializeRecords();[recordsLock lock];BettyRFCOMMRecord *record=[[records objectForKey:@(identifier)] retain];[recordsLock unlock];return [record autorelease];}

long long betty_rfcomm_open(const char *address,const char *serviceUUID,int *error){@autoreleasepool{initializeRecords();IOBluetoothDevice *device=[IOBluetoothDevice deviceWithAddressString:[NSString stringWithUTF8String:address]];if(!device){*error=kIOReturnBadArgument;return 0;}NSUUID *parsed=[[NSUUID alloc] initWithUUIDString:[NSString stringWithUTF8String:serviceUUID]];if(!parsed){*error=kIOReturnBadArgument;return 0;}uuid_t bytes;[parsed getUUIDBytes:bytes];[parsed release];IOBluetoothSDPUUID *uuid=[IOBluetoothSDPUUID uuidWithBytes:bytes length:sizeof(bytes)];IOBluetoothSDPServiceRecord *service=[device getServiceRecordForUUID:uuid];if(!service){IOReturn query=[device performSDPQuery:nil];if(query!=kIOReturnSuccess){*error=query;return 0;}service=[device getServiceRecordForUUID:uuid];}BluetoothRFCOMMChannelID channelID=0;if(!service||[service getRFCOMMChannelID:&channelID]!=kIOReturnSuccess||channelID==0){*error=kIOReturnNotFound;return 0;}BettyRFCOMMRecord *record=[BettyRFCOMMRecord new];IOBluetoothRFCOMMChannel *channel=nil;IOReturn result=[device openRFCOMMChannelSync:&channel withChannelID:channelID delegate:record];if(result!=kIOReturnSuccess){[record release];*error=result;return 0;}record.channel=channel;[recordsLock lock];long long identifier=nextID++;[records setObject:record forKey:@(identifier)];[recordsLock unlock];[record release];*error=0;return identifier;}}
int betty_rfcomm_read(long long identifier,void *output,int maximum,int timeout_ms,int *error){@autoreleasepool{BettyRFCOMMRecord *record=recordFor(identifier);if(!record){*error=kIOReturnNotFound;return 0;}[record.condition lock];if(record.buffer.length==0&&!record.closed){[record.condition waitUntilDate:[NSDate dateWithTimeIntervalSinceNow:(double)timeout_ms/1000.0]];}if(record.buffer.length==0){BOOL closed=record.closed;[record.condition unlock];*error=closed?kIOReturnNotOpen:0;return 0;}NSUInteger count=MIN((NSUInteger)maximum,record.buffer.length);memcpy(output,record.buffer.bytes,count);[record.buffer replaceBytesInRange:NSMakeRange(0,count) withBytes:NULL length:0];[record.condition unlock];*error=0;return (int)count;}}
int betty_rfcomm_write(long long identifier,void *data,int length){@autoreleasepool{BettyRFCOMMRecord *record=recordFor(identifier);if(!record)return kIOReturnNotFound;if(length<0||length>UINT16_MAX)return kIOReturnBadArgument;return (int)[record.channel writeSync:data length:(UInt16)length];}}
int betty_rfcomm_close(long long identifier){@autoreleasepool{initializeRecords();[recordsLock lock];BettyRFCOMMRecord *record=[[records objectForKey:@(identifier)] retain];[records removeObjectForKey:@(identifier)];[recordsLock unlock];if(!record)return kIOReturnNotFound;IOReturn result=[record.channel closeChannel];[record release];return (int)result;}}
